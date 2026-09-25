package main

import (
	"encoding/json"
	"errors"
	"flag"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
	"io"
)

func runToolsCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("tools", flag.ContinueOnError)
	flags.SetOutput(stderr)
	format := flags.String("format", "summary", "summary, mcp or openai")
	group := flags.String("group", "", "optional exact capability group")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: agentdock tools [--format summary|mcp|openai] [--group id]")
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	result, err := app.ExportToolCatalog(cfg, app.CatalogRequest{Format: *format, Group: *group})
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(result)
}
