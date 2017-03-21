package cmd

import (
	"fmt"
	"strings"

	"github.com/heartlock/ovn-kubernetes/pkg/common"
	"github.com/heartlock/ovn-kubernetes/pkg/exec"
	"github.com/spf13/cobra"
)

func InitMaster() *cobra.Command {

	var MasterCmd = &cobra.Command{
		Use:   "master [no options!]",
		Short: "init ovn master",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initMaster(cmd, args); err != nil {
				return fmt.Errorf("failed init minion: %v", err)
			}
			return nil
		},
	}

	MasterCmd.Flags().StringP("cluster-ip-subnet", "", "", "The cluster wide larger subnet of private ip addresses.")
	MasterCmd.Flags().StringP("master-switch-subnet", "", "", "The smaller subnet just for master.")
	MasterCmd.Flags().StringP("node-name", "", "", "A unique node name.")

	return MasterCmd
}

func initMaster(cmd *cobra.Command, args []string) error {
	_, err := fetchOVNNB()
	if err != nil {
		return err
	}

	masterSwitchSubnet := cmd.Flags().Lookup("master-switch-subnet").Value.String()
	if masterSwitchSubnet == "" {
		return fmt.Errorf("argument --master-switch-subnet should be non-null")
	}

	clusterIpSubnet := cmd.Flags().Lookup("cluster-ip-subnet").Value.String()
	if clusterIpSubnet == "" {
		return fmt.Errorf("argument --cluster-ip-subnet should be non-null")
	}

	nodeName := cmd.Flags().Lookup("node-name").Value.String()
	if nodeName == "" {
		return fmt.Errorf("argument --cluster-ip-subnet should be non-null")
	}

	// Create a single common distributed router for the cluster.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lr-add", nodeName, "--", "set", "logical_router", nodeName, "external_ids:k8s-cluster-router=yes")
	if err != nil {
		return fmt.Errorf("failed create single common distributed router: %v", err)
	}

	// Create 2 load-balancers for east-west traffic.  One handles UDP and another handles TCP.
	re, err := exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=_uuid", "find", "load_balancer", "external_ids:k8s-cluster-lb-tcp=yes")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed find k8sClusterLbTcp")
	}
	k8sClusterLbTcp := re[0]
	if k8sClusterLbTcp == "" {
		_, err = exec.RunCommand("ovn-nbctl", "--", "create", "load_balancer", "external_ids:k8s-cluster-lb-tcp=yes")
		if err != nil {
			return fmt.Errorf("failed create tcp load-balancer: %v", err)
		}
	}

	re, err = exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=_uuid", "find", "load_balancer", "external_ids:k8s-cluster-lb-udp=yes")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed find k8sClusterLbUdp")
	}
	k8sClusterLbUdp := re[0]
	if k8sClusterLbUdp == "" {
		_, err = exec.RunCommand("ovn-nbctl", "--", "create", "load_balancer", "external_ids:k8s-cluster-lb-udp=yes", "protocol=udp")
		if err != nil {
			return fmt.Errorf("failed create udp load-balancer: %v", err)
		}
	}

	// Create a logical switch called "join" that will be used to connect gateway routers to the distributed router.
	// The "join" will be allocated IP addresses in the range 100.64.1.0/24
	_, err = exec.RunCommand("ovn-nbctl", "--may-exist", "ls-add", "join")
	if err != nil {
		return fmt.Errorf("failed create logical switch called join: %v", err)
	}
	// Connect the distributed router to "join"
	re, err = exec.RunCommand("ovn-nbctl", "--if-exist", "get", "logical_router_port", "rtoj-"+nodeName, "mac")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed get rtoj-%v mac", nodeName)
	}
	routerMac := strings.Trim(re[0], "\"")
	if routerMac == "" {
		routerMac = common.GenerateMac()
		_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lrp-add", nodeName, "rtoj-"+nodeName, routerMac, "100.64.1.1/24", "--", "set", "logical_router_port", "rtoj-"+nodeName, "external_ids:connect_to_join=yes")
		if err != nil {
			return fmt.Errorf("failed add port rtoj-%v : %v", nodeName, err)
		}
	}

	// Connect the switch "join" to the router.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lsp-add", "join", "jtor-"+nodeName, "--", "set", "logical_switch_port", "jtor-"+nodeName, "type=router", "options:router-port=rtoj-"+nodeName, "addresses="+"\""+routerMac+"\"")

	err = createManagementPort(nodeName, masterSwitchSubnet, clusterIpSubnet)
	if err != nil {
		return fmt.Errorf("failed create management port: %v", err)
	}
	return nil
}
