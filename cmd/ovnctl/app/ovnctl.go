package app

import (
	"github.com/mozhuli/ovn-stackube/pkg/ovnctl/cmd"
	"github.com/spf13/cobra"
)

func Run() error {
	rootCmd := &cobra.Command{
		Use:   "ovnctl [string to echo]",
		Short: "run ovnctl",
		Long:  `run ovnctl to init master, minion, gateway`,
	}

	masterCmd := cmd.InitMaster()
	minionCmd := cmd.InitMinion()
	gatewayCmd := cmd.InitGateway()

	rootCmd.AddCommand(masterCmd)
	rootCmd.AddCommand(minionCmd)
	rootCmd.AddCommand(gatewayCmd)

	return rootCmd.Execute()
}
