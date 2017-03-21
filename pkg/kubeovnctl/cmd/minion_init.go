package cmd

import (
	"fmt"
	"os"
	osExec "os/exec"

	"github.com/heartlock/ovn-kubernetes/pkg/exec"
	"github.com/spf13/cobra"
)

func InitMinion() *cobra.Command {

	var MinionCmd = &cobra.Command{
		Use:   "minion [no options!]",
		Short: "init ovn minion",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initMinion(cmd, args); err != nil {
				return fmt.Errorf("failed init minion: %v", err)
			}
			return nil
		},
	}

	MinionCmd.Flags().StringP("cluster-ip-subnet", "", "", "The cluster wide larger subnet of private ip addresses.")
	MinionCmd.Flags().StringP("minion-switch-subnet", "", "", "The smaller subnet just for this master.")
	MinionCmd.Flags().StringP("node-name", "", "", "A unique node name.")

	return MinionCmd
}

func initMinion(cmd *cobra.Command, args []string) error {

	_, err := fetchOVNNB()
	if err != nil {
		return err
	}
	minionSwitchSubnet := cmd.Flags().Lookup("minion-switch-subnet").Value.String()
	if minionSwitchSubnet == "" {
		return fmt.Errorf("failed get minion-switch-subnet")
	}

	clusterIpSubnet := cmd.Flags().Lookup("cluster-ip-subnet").Value.String()
	if clusterIpSubnet == "" {
		return fmt.Errorf("failed get cluster-ip-subnet")
	}

	nodeName := cmd.Flags().Lookup("node-name").Value.String()
	if nodeName == "" {
		return fmt.Errorf("failed get node-name")
	}

	cniPluginPath, err := osExec.LookPath(CNI_PLUGIN)
	if err != nil {
		return fmt.Errorf("no cni plugin %v found", CNI_PLUGIN)
	}

	_, err = os.Stat(CNI_LINK_PATH)
	if err != nil && !os.IsExist(err) {
		err = os.MkdirAll(CNI_LINK_PATH, os.ModeDir)
		if err != nil {
			return err
		}
	}
	cniFile := CNI_LINK_PATH + "/ovn_cni"
	_, err = os.Stat(cniFile)
	if err != nil && !os.IsExist(err) {
		_, err = exec.RunCommand("ln", "-s", cniPluginPath, cniFile)
		if err != nil {
			return err
		}
	}

	_, err = os.Stat(CNI_CONF_PATH)
	if err != nil && !os.IsExist(err) {
		err = os.MkdirAll(CNI_CONF_PATH, os.ModeDir)
		if err != nil {
			return err
		}
	}

	// Create the CNI config
	cniConf := CNI_CONF_PATH + "/10-net.conf"
	_, err = os.Stat(cniConf)
	if err != nil && !os.IsExist(err) {
		// TODO:verify if it is needed to set config file in 10-net.conf
		data := "{\"bridge\": \"br-int\", \"ipMasq\": \"false\", \"name\": \"net\", \"ipam\": {\"subnet\": \"" + minionSwitchSubnet + "\", \"type\": \"host-local\"}, \"isGateway\": \"true\", \"type\": \"ovn_cni\"}"
		f, err := os.OpenFile(cniConf, os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			return err
		}
		fmt.Fprintf(f, data)
		defer f.Close()

	}

	err = createManagementPort(nodeName, minionSwitchSubnet, clusterIpSubnet)
	if err != nil {
		return err
	}
	return nil

}
