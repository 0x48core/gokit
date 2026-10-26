package codec

import (
	jsoniter "github.com/json-iterator/go"
)

type Serializer interface {
	Serialize(data any) (string, error)
	Deserialize(data string, target any) error
}

type JSONSerializer struct{}

func (s JSONSerializer) Serialize(data any) (string, error) {
	b, e := jsoniter.Marshal(data)
	return string(b), e
}

func (s JSONSerializer) Deserialize(data string, target any) error {
	return jsoniter.Unmarshal([]byte(data), target)
}
