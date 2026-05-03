package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
)

//go:embed default_config.toml
var defaultConfigFile []byte

type Config struct {
	DefaultNotification string   `toml:"default_notification"`
	Notification        string   `toml:"notification"`
	NotificationCommand []string `toml:"notification_command"`
}

func builtinConfig() Config {
	return Config{
		DefaultNotification: "POMO time is UP",
		Notification:        `time is up: {{.Comment}}`,
		NotificationCommand: []string{"osascript", "-e", `display dialog "{{.Message}}"`},
	}
}

func loadConfig() Config {
	cfg := builtinConfig()
	path := configPath()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(pomoDir(), 0700); err == nil {
			os.WriteFile(path, defaultConfigFile, 0644)
		}
		return cfg
	}

	toml.DecodeFile(path, &cfg)
	return cfg
}

type templateData struct {
	Comment string
	Minutes int
	Elapsed string
	Message string
}

func runNotification(cfg Config, comment string, minutes int, elapsed time.Duration) {
	data := templateData{
		Comment: comment,
		Minutes: minutes,
		Elapsed: fmt.Sprintf("%02d:%02d", int(elapsed.Minutes()), int(elapsed.Seconds())%60),
	}

	tmplStr := cfg.DefaultNotification
	if comment != "" {
		tmplStr = cfg.Notification
	}
	data.Message = renderTemplate(tmplStr, data)

	args := make([]string, len(cfg.NotificationCommand))
	for i, arg := range cfg.NotificationCommand {
		args[i] = renderTemplate(arg, data)
	}

	if len(args) > 0 {
		execNotify(args[0], args[1:]...)
	}
}

func renderTemplate(tmplStr string, data templateData) string {
	t, err := template.New("").Parse(tmplStr)
	if err != nil {
		return tmplStr
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return tmplStr
	}
	return buf.String()
}
