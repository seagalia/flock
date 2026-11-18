package measure

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

// Knob is LOAD_<name> when set, otherwise def scaled by LOAD_SCALE, never below 1
func Knob(name string, def int) (int, error) {
	variable := "LOAD_" + name
	raw := os.Getenv(variable)
	if raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return 0, fmt.Errorf("%s=%q: want a positive integer", variable, raw)
		}
		return value, nil
	}
	scale, err := scaleFactor()
	if err != nil {
		return 0, err
	}
	return max(1, int(math.Round(float64(def)*scale))), nil
}

// Duration is LOAD_<name> when set, otherwise def: durations are never scaled
func Duration(name string, def time.Duration) (time.Duration, error) {
	variable := "LOAD_" + name
	raw := os.Getenv(variable)
	if raw == "" {
		return def, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s=%q: want a positive duration", variable, raw)
	}
	return value, nil
}

// Scale is LOAD_SCALE as given, for the report
func Scale() string {
	raw := os.Getenv("LOAD_SCALE")
	if raw == "" {
		return "1"
	}
	return raw
}

func scaleFactor() (float64, error) {
	raw := Scale()
	scale, err := strconv.ParseFloat(raw, 64)
	if err != nil || scale <= 0 {
		return 0, fmt.Errorf("LOAD_SCALE=%q: want a positive number", raw)
	}
	return scale, nil
}
