package driverwire

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/wire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// Build makes the module's driver from the settings the operator wrote and
// the grants the host gave the module, such as its data directory.
type Build func(settings json.RawMessage, grants wirerpc.Grants, log *slog.Logger) (connectorapi.Driver, error)

// Options name a driver module and say how to build its driver.
type Options struct {
	Name    string
	Version extension.Version
	Build   Build
	// Logger is where the module logs. The host keeps what it writes to
	// standard error. Nil logs there as text.
	Logger *slog.Logger
}

// Run is the whole main of a driver module. It writes the handshake on
// standard output, answers the host on standard input and output until the
// host goes, and closes every session on the way out.
func Run(ctx context.Context, opts Options) error {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	line, err := wire.Handshake{
		Protocol: wire.ProtocolMajor, MinMinor: wire.MinMinor, MaxMinor: wire.MaxMinor, Address: wire.StdioAddress,
	}.Line()
	if err != nil {
		return fmt.Errorf("driverwire: render the handshake: %w", err)
	}

	if _, err = os.Stdout.WriteString(line); err != nil {
		return fmt.Errorf("driverwire: write the handshake: %w", err)
	}

	serves := wirerpc.Methods{}

	conn, err := wirerpc.NewConn(stdio{}, wirerpc.Options{Role: wirerpc.RoleChild, Methods: serves, Logger: log})
	if err != nil {
		return fmt.Errorf("driverwire: build the session: %w", err)
	}

	module := NewModule(conn, log)
	defer module.Close()

	maps.Copy(serves, module.Handlers())
	maps.Copy(serves, lifecycle(opts, module, log))

	if err = conn.Serve(ctx); err != nil {
		return fmt.Errorf("driverwire: serve: %w", err)
	}

	return nil
}

// lifecycle answers what every module answers. The driver is built when the
// host gives the settings, which it does before any driver call.
func lifecycle(opts Options, module *Module, log *slog.Logger) wirerpc.Methods {
	return wirerpc.Methods{
		wirerpc.MethodInfo: func(context.Context, wirerpc.Call) (any, error) {
			return wirerpc.Info{
				Name: opts.Name, Version: opts.Version, Contract: wirerpc.Contract,
				Serves: append(Methods(), wirerpc.MethodConfigure),
			}, nil
		},
		wirerpc.MethodConfigure: wirerpc.ConfigureGranted(
			func(_ context.Context, settings json.RawMessage, grants wirerpc.Grants) error {
				driver, err := opts.Build(settings, grants, log)
				if err != nil {
					return err
				}

				return module.Use(driver)
			}),
		wirerpc.MethodShutdown: func(context.Context, wirerpc.Call) (any, error) {
			module.Close()

			return nil, nil
		},
	}
}

// stdio is the channel to the host: what it writes arrives on standard input,
// and what the module writes leaves on standard output.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }   //nolint:wrapcheck // the channel's own
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) } //nolint:wrapcheck // the channel's own
func (stdio) Close() error                { return os.Stdin.Close() }   //nolint:wrapcheck // the channel's own
