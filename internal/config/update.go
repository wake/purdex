package config

import (
	"errors"
	"time"
)

// lockWait bounds how long a writer waits for "<path>.lock".
var lockWait = 5 * time.Second

// Update is the one protocol every writer of config.toml shares (#2556). Stub: the red tests come first.
func Update(path string, mutate func(*Config) error) (Config, error) {
	return Config{}, errors.New("not implemented")
}
