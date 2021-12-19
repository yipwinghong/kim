package examples

import (
	"context"
	"flag"
	"github.com/yipwinghong/kim/examples/client"
	"github.com/yipwinghong/kim/examples/server"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const version = "v1"

func Run() {
	flag.Parse()

	root := &cobra.Command{
		Use:     "chat",
		Version: version,
		Short:   "chat demo",
	}
	ctx := context.Background()

	root.AddCommand(server.NewServerCmd(ctx, version))
	root.AddCommand(client.NewClientCmd(ctx))

	if err := root.Execute(); err != nil {
		logrus.WithError(err).Fatal("Could not run command")
	}
}
