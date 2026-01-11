package utils

import (
	"os"
	"strconv"
)

// GetEnvOrDefaultInt returns the integer value of an environment variable,
// or the default value if the variable is not set or cannot be parsed.
func GetEnvOrDefaultInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	intVal, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return intVal
}
