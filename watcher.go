package config

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"text/template"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"golang.org/x/exp/maps"
)

type Options struct {
	RefreshInterval time.Duration
	PathParameters  map[string]string
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

func WithPathParameter(name string, value string) Option {
	return func(o *Options) {
		o.PathParameters[name] = value
	}
}

func WithRefreshInterval(refreshInterval time.Duration) Option {
	return func(o *Options) {
		o.RefreshInterval = refreshInterval
	}
}

type Watcher struct {
	cli            *clientv3.Client
	pathParameters map[string]string

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

func NewWatcher(conf any, options ...Option) (*Watcher, error) {
	opts := &Options{
		RefreshInterval: 5 * time.Second,
		PathParameters:  make(map[string]string),
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
		valueConfigs[name] = valueConfig{
			Path:    etcdPath,
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
		pathParameters:  opts.PathParameters,
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

func (w *Watcher) readCurrentValue(ctx context.Context, name, path string, defaultValue string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, w.etcdTimeout)
	defer cancel()

	tmpl, err := template.New(fmt.Sprintf("config-value-%s", name)).Parse(path)
	if err != nil {
		return "", err
	}
	buf := bytes.Buffer{}
	if err = tmpl.Execute(&buf, w.pathParameters); err != nil {
		return "", err
	}
	response, err := w.cli.Get(ctx, buf.String())
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
