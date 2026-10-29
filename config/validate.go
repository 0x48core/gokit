package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// squashed names an embedded struct whose fields sit in its parent, so it can
// be dropped from error keys.
const squashed = "\x00"

var validate = newValidate()

func newValidate() func(any) error {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(sf reflect.StructField) string {
		name, squash := keyName(sf)
		switch {
		case squash:
			return squashed
		case name == "-":
			return sf.Name // "-" would make the validator skip the field
		}
		return name
	})

	return func(out any) error {
		if err := v.Struct(out); err != nil {
			var verrs validator.ValidationErrors
			if !errors.As(err, &verrs) {
				return err
			}
			errs := make([]error, 0, len(verrs))
			for _, fe := range verrs {
				errs = append(errs, fieldError(fe))
			}
			return errors.Join(errs...)
		}
		if cv, ok := out.(Validator); ok {
			return cv.Validate()
		}
		return nil
	}
}

// fieldError turns "Config.redis.addr" failing "min=1" into
// "redis.addr: failed min=1 validation".
func fieldError(fe validator.FieldError) error {
	parts := strings.Split(fe.Namespace(), ".")[1:]
	key := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != squashed {
			key = append(key, p)
		}
	}

	rule := fe.Tag()
	if fe.Param() != "" {
		rule += "=" + fe.Param()
	}
	return fmt.Errorf("%s: failed %s validation", strings.Join(key, "."), rule)
}
