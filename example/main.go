package main

import (
	"context"
	"fmt"

	etcoen "github.com/tonkeeper/etcoen"
)

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
