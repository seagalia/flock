package params

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/platform/env"
)

// The setters below fill a lego provider config from the string values of the params. An empty value keeps what
// NewDefaultConfig of the provider set.

func setString(dst *string, raw string) {
	if raw == "" {
		return
	}
	*dst = raw
}

func setInt[T int | int32 | int64](dst *T, key, raw string) error {
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	*dst = T(v)
	return nil
}

func setBool(dst *bool, key, raw string) error {
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	*dst = v
	return nil
}

func setDuration(dst *time.Duration, key, raw string) error {
	if raw == "" {
		return nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	*dst = v
	return nil
}

func setURL(dst **url.URL, key, raw string) error {
	if raw == "" {
		return nil
	}
	v, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	*dst = v
	return nil
}

// setList reads a comma separated list.
func setList(dst *[]string, raw string) {
	if raw == "" {
		return
	}
	var values []string
	for v := range strings.SplitSeq(raw, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			values = append(values, v)
		}
	}
	*dst = values
}

// setPairs reads "key1:value1,key2:value2", the format lego reads from its environment variables.
func setPairs(dst *map[string]string, key, raw string) error {
	if raw == "" {
		return nil
	}
	v, err := env.ParsePairs(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	*dst = v
	return nil
}
