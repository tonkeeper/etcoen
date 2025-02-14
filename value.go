package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"go.uber.org/zap"
)

type Value[T any] struct {
	value T
}

func Subscribe[T any](watcher *Watcher, e *Value[T]) (chan T, error) {
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
	if !watcher.subscribe(name, stringCh) {
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
					watcher.logger.Error("failed to decode int value from etcd",
						zap.String("value", str),
						zap.String("name", name))
				}
				ch <- any(value).(T)
			case string:
				ch <- any(str).(T)
			default:
				var value T
				if err := json.Unmarshal([]byte(str), &value); err != nil {
					watcher.logger.Error("failed to decode json value from etcd",
						zap.String("value", str),
						zap.String("name", name))
				}
				ch <- value
			}
		}
	}()
	return ch, nil
}
