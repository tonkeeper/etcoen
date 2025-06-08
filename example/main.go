package main

import (
	"context"
	"fmt"
	"reflect"

	"github.com/caarlos0/env/v6"

	"github.com/tonkeeper/etcoen"
)

type JsonStruct struct {
	A int
	B int
}

type Config struct {
	DeploymentGroup string `env:"DEPLOYMENT_GROUP" envDefault:"hello-group"`

	IntValue    etcoen.Value[int]        `etcd:"/app-config/{{.DeploymentGroup}}/int_value" etcdDefault:"17"`
	StringValue etcoen.Value[string]     `etcd:"/app-config/{{.DeploymentGroup}}/string_value" etcdDefault:"say-hello"`
	JsonValue   etcoen.Value[JsonStruct] `etcd:"/app-config/{{.DeploymentGroup}}/json_value" etcdDefault:"{}"`
}

func main() {

	c := Config{}
	if err := env.ParseWithFuncs(&c, map[reflect.Type]env.ParserFunc{}); err != nil {
		panic(err)
	}

	watcher, err := etcoen.NewWatcher(&c,
		etcoen.WithPathParameter("DeploymentGroup", c.DeploymentGroup),
	)
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
