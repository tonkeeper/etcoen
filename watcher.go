package config

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"golang.org/x/exp/maps"
)

type Options struct {
	RefreshInterval time.Duration
	EtcdUsername    string
	EtcdPassword    string
	EtcdCaPath      string
	EtcdEndpoints   []string
}

type Option func(*Options)

func WithEtcdConnection(username, password, caPath string, endpoints []string) Option {
	return func(o *Options) {
		o.EtcdUsername = username
		o.EtcdPassword = password
		o.EtcdCaPath = caPath
		o.EtcdEndpoints = endpoints
	}
}

func WithRefreshInterval(refreshInterval time.Duration) Option {
	return func(o *Options) {
		o.RefreshInterval = refreshInterval
	}
}

type Watcher struct {
	cli             *clientv3.Client
	refreshInterval time.Duration
	etcdTimeout     time.Duration

	fields map[string]any

	mu           sync.RWMutex
	valueConfigs map[string]valueConfig
	subscribers  map[string]chan string
}

type valueConfig struct {
	Path    string
	Default string
}

// parseEnv fills struct fields marked with `env`, `envDefault` and
// `envSeparator` tags from the environment.
func parseEnv(target any) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("env target must be pointer to struct")
	}
	return parseEnvStruct(v.Elem())
}

func parseEnvStruct(v reflect.Value) error {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		ft := t.Field(i)
		if ft.Anonymous && field.Kind() == reflect.Struct {
			if err := parseEnvStruct(field); err != nil {
				return err
			}
			continue
		}
		envName := ft.Tag.Get("env")
		if envName == "" {
			continue
		}
		val, ok := os.LookupEnv(envName)
		if !ok {
			val = ft.Tag.Get("envDefault")
			if val == "" {
				continue
			}
		}
		sep := ft.Tag.Get("envSeparator")
		if err := setValueFromString(field, val, sep); err != nil {
			return fmt.Errorf("field %s: %w", ft.Name, err)
		}
	}
	return nil
}

func setValueFromString(field reflect.Value, val, sep string) error {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		return setValueFromString(field.Elem(), val, sep)
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(val)
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return err
		}
		field.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if field.Type().PkgPath() == "" && field.Type().Name() == "Duration" {
			d, err := time.ParseDuration(val)
			if err != nil {
				return err
			}
			field.SetInt(int64(d))
			return nil
		}
		i, err := strconv.ParseInt(val, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(val, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(u)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(val, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(f)
	case reflect.Slice:
		if sep == "" {
			sep = ","
		}
		parts := strings.Split(val, sep)
		slice := reflect.MakeSlice(field.Type(), len(parts), len(parts))
		for i, p := range parts {
			if err := setValueFromString(slice.Index(i), strings.TrimSpace(p), ""); err != nil {
				return err
			}
		}
		field.Set(slice)
	case reflect.Struct:
		// currently not supported except time.Duration above
		return fmt.Errorf("unsupported kind %s", field.Kind())
	default:
		return fmt.Errorf("unsupported kind %s", field.Kind())
	}
	return nil
}

func NewWatcher(conf any, options ...Option) (*Watcher, error) {
	opts := &Options{
		RefreshInterval: 5 * time.Second,
	}
	for _, o := range options {
		o(opts)
	}

	// If etcd connection parameters are not provided, read them from environment variables
	if len(opts.EtcdEndpoints) == 0 {
		if endpoints := os.Getenv("ETCD_ENDPOINTS"); endpoints != "" {
			// Split by comma as per the example's envSeparator
			opts.EtcdEndpoints = strings.Split(endpoints, ",")
		}
	}
	if opts.EtcdUsername == "" {
		opts.EtcdUsername = os.Getenv("ETCD_USER")
	}
	if opts.EtcdPassword == "" {
		opts.EtcdPassword = os.Getenv("ETCD_PASSWORD")
	}
	if opts.EtcdCaPath == "" {
		opts.EtcdCaPath = os.Getenv("ETCD_CA_CERT_PATH")
		// Set default value for ETCD_CA_CERT_PATH if not provided
		if opts.EtcdCaPath == "" {
			opts.EtcdCaPath = "tls/ca.crt"
		}
	}

	val := reflect.ValueOf(conf)
	if val.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("conf must be a pointer to a struct")
	}

	if err := parseEnv(conf); err != nil {
		return nil, err
	}

	etcdFields := make(map[string]any)
	valueConfigs := make(map[string]valueConfig)

	typ := reflect.TypeOf(conf).Elem()
	for i := 0; i < val.Elem().NumField(); i++ {
		fieldTag := typ.Field(i).Tag
		etcdPath := fieldTag.Get("etcd")
		if len(etcdPath) == 0 {
			continue
		}
		name := typ.Field(i).Name
		address := val.Elem().Field(i).Addr().Interface()
		// Store the field name and its address in the map
		etcdFields[name] = address

		// Substitute variables in the path during initialization
		substitutedPath, err := substitutePathVariables(etcdPath)
		if err != nil {
			return nil, fmt.Errorf("failed to substitute variables in path for field %s: %w", name, err)
		}

		valueConfigs[name] = valueConfig{
			Path:    substitutedPath,
			Default: fieldTag.Get("etcdDefault"),
		}
	}
	var tlsConfig *tls.Config
	if opts.EtcdCaPath != "DISABLE" && len(opts.EtcdCaPath) > 0 {
		caCert, err := os.ReadFile(opts.EtcdCaPath)
		if err != nil {
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig = &tls.Config{
			RootCAs: caCertPool,
		}
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   opts.EtcdEndpoints,
		DialTimeout: 2 * time.Second,
		Username:    opts.EtcdUsername,
		Password:    opts.EtcdPassword,
		TLS:         tlsConfig,
	})
	if err != nil {
		return nil, err
	}
	return &Watcher{
		cli:             cli,
		fields:          etcdFields,
		valueConfigs:    valueConfigs,
		subscribers:     map[string]chan string{},
		refreshInterval: opts.RefreshInterval,
		etcdTimeout:     2 * time.Second,
	}, nil
}

func (w *Watcher) subscribe(name string, ch chan string, defaultValue *string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, ok := w.subscribers[name]; ok {
		return false
	}
	w.subscribers[name] = ch
	if defaultValue != nil {
		w.valueConfigs[name] = valueConfig{
			Path:    w.valueConfigs[name].Path,
			Default: *defaultValue,
		}
	}
	return true
}

func (w *Watcher) getSubscriber(name string) (chan string, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	ch, ok := w.subscribers[name]
	return ch, ok
}

// Regular expression to find patterns like {{VAR_NAME}}
var re = regexp.MustCompile(`\{\{\.?([A-Za-z0-9_]+)\}\}`)

// substitutePathVariables replaces variable patterns in the path with their environment values
func substitutePathVariables(path string) (string, error) {
	// Find all matches in the path
	matches := re.FindAllStringSubmatch(path, -1)

	// Process each match
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		// Extract the variable name
		varName := match[1]

		// Look up the variable in the environment
		envValue := os.Getenv(varName)
		if envValue == "" {
			return "", fmt.Errorf("environment variable %s not found", varName)
		}

		// Replace the pattern with the environment value
		path = strings.Replace(path, match[0], envValue, -1)
	}

	return path, nil
}

func (w *Watcher) readCurrentValue(ctx context.Context, name, path string, defaultValue string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, w.etcdTimeout)
	defer cancel()

	// Query etcd with the path (which should already be processed)
	response, err := w.cli.Get(ctx, path)
	if err != nil {
		return "", err
	}
	if len(response.Kvs) == 0 {
		return defaultValue, nil
	}
	return string(response.Kvs[0].Value), nil
}

func (w *Watcher) configs() map[string]valueConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return maps.Clone(w.valueConfigs)
}

func (w *Watcher) Run(ctx context.Context) {
	prevValues := map[string]string{}
	for {
		for name, valueConfig := range w.configs() {
			subCh, ok := w.getSubscriber(name)
			if !ok {
				continue
			}
			currentValue, err := w.readCurrentValue(ctx, name, valueConfig.Path, valueConfig.Default)
			if err != nil {
				continue
			}
			if prevValue, ok := prevValues[name]; ok && prevValue == currentValue {
				continue
			}
			prevValues[name] = currentValue
			subCh <- currentValue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.refreshInterval):
		}
	}

}
