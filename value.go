package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strconv"
)

type Value[T any] struct {
	value T
}

type SubscribeOptions struct {
	Default *string
}

type SubscribeOption func(*SubscribeOptions)

func OverrideDefault(value string) SubscribeOption {
	return func(options *SubscribeOptions) {
		options.Default = &value
	}
}

func Subscribe[T any](watcher *Watcher, e *Value[T], opts ...SubscribeOption) (chan T, error) {
	options := &SubscribeOptions{}
	for _, o := range opts {
		o(options)
	}
	ptrValue := reflect.ValueOf(e)
	if ptrValue.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("the provided value must be a pointer")
	}
	var name string
	ptrAddress := ptrValue.Pointer()
	for fieldName, address := range watcher.fields {
		if ptrAddress == reflect.ValueOf(address).Pointer() {
			name = fieldName
			break
		}
	}
	if len(name) == 0 {
		return nil, fmt.Errorf("the provided value is not a field of the config struct")
	}
	stringCh := make(chan string)
	if !watcher.subscribe(name, stringCh, options.Default) {
		return nil, fmt.Errorf("you are already subscribed to this config variable")
	}
	ch := make(chan T)
	go func() {
		defer close(ch)
		for str := range stringCh {
			switch any(e.value).(type) {
			case int:
				value, err := strconv.Atoi(str)
				if err != nil {
					slog.Default().Error("failed to decode int value from etcd",
						"value", str,
						"name", name)
				}
				ch <- any(value).(T)
			case string:
				ch <- any(str).(T)
			default:
				var value T
				if err := json.Unmarshal([]byte(str), &value); err != nil {
					slog.Default().Error("failed to decode json value from etcd",
						"value", str,
						"name", name)
				}
				ch <- value
			}
		}
	}()
	return ch, nil
}
