# Description

This library helps to read configuration from etcd and watch for changes.

# ETCD Credentials

The library automatically reads ETCD connection parameters from environment variables:
- `ETCD_ENDPOINTS` - comma-separated list of ETCD endpoints
- `ETCD_USER` - ETCD username
- `ETCD_PASSWORD` - ETCD password
- `ETCD_CA_CERT_PATH` - path to CA certificate file (defaults to "tls/ca.crt" if not provided. "DISABLE" to disable TLS)

You can also set these parameters programmatically using the `WithEtcdConnection` option if needed.

Paths in struct tags may include placeholders in the form `{{ENV_NAME}}`. They will be replaced with the value of the corresponding environment variable when the watcher starts.

# Example

```golang

type JsonStruct struct {
        A int
        B int
}

type Config struct {
        NonEtcdVariable string `env:"NON_ETCD_VARIABLE"`

        IntValue    etcoen.Value[int]        `etcd:"/app-config/{{DEPLOYMENT_GROUP}}/int_value" etcdDefault:"17"`
        StringValue etcoen.Value[string]     `etcd:"/app-config/{{DEPLOYMENT_GROUP}}/string_value" etcdDefault:"say-hello"`
        JsonValue   etcoen.Value[JsonStruct] `etcd:"/app-config/{{DEPLOYMENT_GROUP}}/json_value" etcdDefault:"{}"`
}

func main() {
        c := Config{}
        if err := env.ParseWithFuncs(&c, map[reflect.Type]env.ParserFunc{}); err != nil {
                panic(err)
        }

        watcher, err := etcoen.NewWatcher(&c)
        if err != nil {
                panic(err)
        }
        intCh, err := etcoen.Subscribe(watcher, &c.IntValue)
        if err != nil {
                panic(err)
        }
        strCh, err := etcoen.Subscribe(watcher, &c.StringValue, etcoen.OverrideDefault("say-goodbye"))
        if err != nil {
                panic(err)
        }
        jsonCh, err := etcoen.Subscribe(watcher, &c.JsonValue)
        if err != nil {
                panic(err)
        }

        go watcher.Run(context.Background())

        for {
                select {
                case v := <-intCh:
                        fmt.Printf("int value: %d\n", v)
                case v := <-strCh:
                        fmt.Printf("string value: %s\n", v)
                case v := <-jsonCh:
                        fmt.Printf("a: %d, b: %d\n", v.A, v.B)
                }
        }
}

```
