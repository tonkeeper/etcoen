
# Description

This library helps to read configuration from etcd and watch for changes. 

# Example

```
golang

type JsonStruct struct {
	A int
	B int
}

type Config struct {
	EtcdEndpoints []string `env:"ETCD_ENDPOINTS" envSeparator:","`
	EtcdUser      string   `env:"ETCD_USER"`
	EtcdPassword  string   `env:"ETCD_PASSWORD"`
	EtcdCaPath    string   `env:"ETCD_CA_CERT_PATH"`

	DeploymentGroup string `env:"DEPLOYMENT_GROUP" envDefault:"hello-group"`

	IntValue    etcdConfig.Value[int]        `etcd:"/app-config/{{.DeploymentGroup}}/int_value" etcdDefault:"17"`
	StringValue etcdConfig.Value[string]     `etcd:"/app-config/{{.DeploymentGroup}}/string_value" etcdDefault:"say-hello"`
	JsonValue   etcdConfig.Value[JsonStruct] `etcd:"/app-config/{{.DeploymentGroup}}/json_value" etcdDefault:"{}"`
}

func main() {

	c := Config{}
	if err := env.ParseWithFuncs(&c, map[reflect.Type]env.ParserFunc{}); err != nil {
		panic(err)
	}

	watcher, err := etcdConfig.NewWatcher(&c,
		etcdConfig.WithEtcdConnection(c.EtcdUser, c.EtcdPassword, c.EtcdCaPath, c.EtcdEndpoints),
		etcdConfig.WithPathParameter("DeploymentGroup", c.DeploymentGroup),
	)
	if err != nil {
		panic(err)
	}
	intCh, err := etcdConfig.Subscribe(watcher, &c.IntValue)
	if err != nil {
		panic(err)
	}
	strCh, err := etcdConfig.Subscribe(watcher, &c.StringValue, etcdConfig.OverrideDefault("say-goodbye"))
	if err != nil {
		panic(err)
	}
	jsonCh, err := etcdConfig.Subscribe(watcher, &c.JsonValue)
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
