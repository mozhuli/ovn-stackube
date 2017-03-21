package cmd

import (
	"os"

	"fmt"
	"net"
	"strings"

	"github.com/heartlock/ovn-kubernetes/pkg/common"
	"github.com/heartlock/ovn-kubernetes/pkg/exec"
	"github.com/spf13/cobra"
)

func InitGateway() *cobra.Command {

	var GatewayCmd = &cobra.Command{
		Use:   "gateway [no options!]",
		Short: "init ovn gateway",
		PreRun: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Inside subCmd PreRun with args: %v\n", args)
		},
		Run: func(cmd *cobra.Command, args []string) {
			if err := initGateway(cmd, args); err != nil {
				fmt.Fprint(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Printf("Inside subCmd Run with args: %v\n", args)
		},
		PostRun: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Inside subCmd PostRun with args: %v\n", args)
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Inside subCmd PersistentPostRun with args: %v\n", args)
		},
	}

	GatewayCmd.Flags().StringP("cluster-ip-subnet", "", "", "The cluster wide larger subnet of private ip addresses.")
	GatewayCmd.Flags().StringP("physical-interface", "", "", "The physical interface via which external connectivity is provided.")
	GatewayCmd.Flags().StringP("bridge-interface", "", "", "The OVS bridge interface via which external connectivity is provided.")
	GatewayCmd.Flags().StringP("physical-ip", "", "", "The ip address of the physical interface or bridge interface via which external connectivity is provided. This should be of the form IP/MASK.")
	GatewayCmd.Flags().StringP("node-name", "", "", "A unique node name.")
	GatewayCmd.Flags().StringP("default-gw", "", "", "The next hop IP address for your physical interface.")
	GatewayCmd.Flags().StringP("rampout-ip-subnets", "", "", "Uses this gateway to rampout traffic originating from the specified comma separated ip subnets.  Used to distribute outgoing traffic via multiple gateways.")

	return GatewayCmd
}

func initGateway(cmd *cobra.Command, args []string) error {
	clusterIpSubnet := cmd.Flags().Lookup("cluster-ip-subnet").Value.String()
	if clusterIpSubnet == "" {
		return fmt.Errorf("failed get cluster-ip-subnet")
	}

	nodeName := cmd.Flags().Lookup("node-name").Value.String()
	if nodeName == "" {
		return fmt.Errorf("failed get node-name")
	}

	physicalIp := cmd.Flags().Lookup("physical-ip").Value.String()
	if physicalIp == "" {
		return fmt.Errorf("failed get physical-ip")
	}

	physicalInterface := cmd.Flags().Lookup("physical-interface").Value.String()
	bridgeInterface := cmd.Flags().Lookup("bridge-interface").Value.String()
	defaultGW := cmd.Flags().Lookup("default-gw").Value.String()
	rampoutIPSubnet := cmd.Flags().Lookup("rampout-ip-subnet").Value.String()

	// We want either of args.physical_interface or args.bridge_interface
	// provided. But not both. (XOR)
	if (len(physicalInterface) == 0 && len(bridgeInterface) == 0) || (len(physicalInterface) != 0 && len(bridgeInterface) != 0) {
		return fmt.Errorf("One of physical-interface or bridge-interface has to be specified")
	}

	ip, physicalIpNet, _ := net.ParseCIDR(physicalIp)
	n, _ := physicalIpNet.Mask.Size()
	physicalIpMask := fmt.Sprintf("%s\\%d", ip.String(), n)
	physicalIp = ip.String()

	if defaultGW != "" {
		defaultgwByte := net.ParseIP(defaultGW)
		defaultGW = defaultgwByte.String()
	}
	_, err := fetchOVNNB()
	if err != nil {
		return err
	}

	k8sClusterRouter, err := getK8sClusterRouter()
	if err != nil {
		return err
	}

	systemID, err := getLocalSystemID()
	if err != nil {
		return err
	}
	//Find if gateway routers have been created before.
	firstGW := "no"
	re, err := exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=_uuid", "find", "logical_router", "options:chassis!=null")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get physicalGW")
	}
	physicalGW := re[0]
	if physicalGW == "" {
		firstGW = "yes"
	}
	// Create a gateway router.
	gatewayRouter := "GR_" + nodeName
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lr-add", gatewayRouter, "--", "set", "logical_router", gatewayRouter, "options:chassis="+systemID, "external_ids:physical_ip="+physicalIp, "external_ids:first_gateway="+firstGW)
	if err != nil {
		return err
	}

	// Connect gateway router to switch "join".
	re, err = exec.RunCommand("ovn-nbctl", "--if-exist", "get", "logical_router_port", "rtoj-"+gatewayRouter, "mac")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get routerMac")
	}
	routerMac := strings.Trim(re[0], "\"")
	routerIP := ""
	if routerMac == "" {
		routerMac = common.GenerateMac()
		routerIP, err := generateGatewayIP()
		if err != nil {
			return err
		}

		_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lrp-add", gatewayRouter, "rtoj-"+gatewayRouter, routerMac, routerIP, "--", "set", "logical_router_port", "rtoj-"+gatewayRouter, "external_ids:connect_to_join=yes")
		if err != nil {
			return err
		}
	}

	// Connect the switch "join" to the router.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lsp-add", "join", "jtor-"+gatewayRouter, "--", "set", "logical_switch_port", "jtor-"+gatewayRouter, "type=router", "options:router-port=rtoj-"+gatewayRouter, "addresses="+"\""+routerMac+"\"")
	if err != nil {
		return err
	}

	// Add a static route in GR with distributed router as the nexthop.
	_, err = exec.RunCommand("ovn-nbctl", "--may-exist", "lr-route-add", gatewayRouter, clusterIpSubnet, "100.64.1.1")

	// Add a static route in GR with physical gateway as the default next hop.
	if defaultGW != "" {
		_, err = exec.RunCommand("ovn-nbctl", "--may-exist", "lr-route-add", gatewayRouter, "0.0.0.0/0", defaultGW)
		if err != nil {
			return err
		}
	}

	// Add a default route in distributed router with first GR as the nexthop.
	_, err = exec.RunCommand("ovn-nbctl", "--may-exist", "lr-route-add", k8sClusterRouter, "0.0.0.0/0", "100.64.1.2")
	if err != nil {
		return err
	}

	// Create 2 load-balancers for north-south traffic for each gateway router.  One handles UDP and another handles TCP.
	re, err = exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=_uuid", "find", "load_balancer", "external_ids:TCP_lb_gateway_router="+gatewayRouter)
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get k8sNSLbTcp")
	}
	k8sNSLbTcp := strings.Trim(re[0], "'")
	if k8sNSLbTcp != "" {
		_, err = exec.RunCommand("ovn-nbctl", "--", "create", "load_balancer", "external_ids:TCP_lb_gateway_router="+gatewayRouter)
		if err != nil {
			return err
		}
	}

	re, err = exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=_uuid", "find", "load_balancer", "external_ids:UDP_lb_gateway_router="+gatewayRouter)
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get k8sNSLbUdp")
	}
	k8sNSLbUdp := strings.Trim(re[0], "\"")
	if k8sNSLbUdp != "" {
		_, err = exec.RunCommand("ovn-nbctl", "--", "create", "load_balancer", "external_ids:UDP_lb_gateway_router="+gatewayRouter, "protocol=udp")
		if err != nil {
			return err
		}
	}
	//Add north-south load-balancers to the gateway router.
	_, err = exec.RunCommand("ovn-nbctl", "set", "logical_router", gatewayRouter, "load_balancer="+k8sNSLbTcp)
	if err != nil {
		return err
	}
	_, err = exec.RunCommand("ovn-nbctl", "add", "logical_router", gatewayRouter, "load_balancer="+k8sNSLbUdp)
	if err != nil {
		return err
	}

	// Create the external switch for the physical interface to connect to.
	externalSwitch := "ext_" + nodeName
	_, err = exec.RunCommand("ovn-nbctl", "--may-exist", "ls-add", externalSwitch)
	if err != nil {
		return err
	}
	ifaceID := ""
	macAddress := ""
	if physicalInterface != "" {

		// Connect physical interface to br-int. Get its mac address
		ifaceID = physicalInterface + "_" + nodeName
		_, err = exec.RunCommand("ovs-vsctl", "--", "--may-exist", "add-port", "br-int", physicalInterface, "--", "set", "interface", physicalInterface, "external-ids:iface-id="+ifaceID)
		if err != nil {
			return err
		}
		re, err := exec.RunCommand("ovs-vsctl", "--if-exists", "get", "interface", physicalInterface, "mac_in_use")
		if err != nil || re == nil {
			if err != nil {
				return err
			}
			return fmt.Errorf("failed to get macAddress")
		}
		macAddress = strings.Trim(re[0], "\"")

		// Flush the IP address of the physical interface.
		_, err = exec.RunCommand("ip", "addr", "flush", "dev", physicalInterface)
		if err != nil {
			return err
		}
	} else {
		// A OVS bridge's mac address can change when ports are added to it.
		// We cannot let that happen, so make the bridge mac address permanent.
		re, err := exec.RunCommand("ovs-vsctl", "--if-exists", "get", "interface", bridgeInterface, "mac_in_use")
		if err != nil || re == nil {
			if err != nil {
				return err
			}
			return fmt.Errorf("failed to get macAddress")
		}
		macAddress = strings.Trim(re[0], "\"")
		if macAddress == "" {
			return fmt.Errorf("No mac_address found for the bridge-interface")
		}
		_, err = exec.RunCommand("ovs-vsctl", "set", "bridge", bridgeInterface, "other-config:hwaddr="+macAddress)
		if err != nil {
			return err
		}
		ifaceID = bridgeInterface + "_" + nodeName

		// Connect bridge interface to br-int via patch ports.
		patch1 := "k8s-patch-br-int-" + bridgeInterface
		patch2 := "k8s-patch-" + bridgeInterface + "-br-int"

		_, err = exec.RunCommand("ovs-vsctl", "--may-exist", "add-port", bridgeInterface, patch2, "--", "set", "interface", patch2, "type=patch", "options:peer="+patch1)
		if err != nil {
			return err
		}

		_, err = exec.RunCommand("ovs-vsctl", "--may-exist", "add-port", "br-int", patch1, "--", "set", "interface", patch1, "type=patch", "options:peer="+patch2, "external-ids:iface-id="+ifaceID)
		if err != nil {
			return err
		}
	}
	// Add external interface as a logical port to external_switch. This is
	// a learning switch port with "unknown" address.  The external world
	// is accessed via this port.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lsp-add", externalSwitch, ifaceID, "--", "lsp-set-addresses", ifaceID, "unknown")
	if err != nil {
		return err
	}

	// Connect GR to external_switch with mac address of external interface
	// and that IP address.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lrp-add", gatewayRouter, "rtoe-"+gatewayRouter, macAddress, physicalIpMask, "--", "set", "logical_router_port", "rtoe-"+gatewayRouter, "external-ids:gateway-physical-ip=yes")
	if err != nil {
		return err
	}

	// Connect the external_switch to the router.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lsp-add", externalSwitch, "etor-"+gatewayRouter, "--", "set", "logical_switch_port", "etor-"+gatewayRouter, "type=router", "options:router-port=rtoe-"+gatewayRouter, "addresses="+"\""+macAddress+"\"")
	if err != nil {
		return err
	}

	// Default SNAT rules.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--id=@nat", "create", "nat", "type=snat", "logical_ip="+clusterIpSubnet, "external_ip="+physicalIp, "--", "add", "logical_router", gatewayRouter, "nat", "@nat")
	if err != nil {
		return err
	}
	// When there are multiple gateway routers (which would be the likely default for any sane deployment),
	//we need to SNAT traffic heading to the logical space with the Gateway router's IP so that return traffic comes back to the same gateway router.
	if routerIP != "" {
		routerIPByte, _, err := net.ParseCIDR(routerIP)
		if err != nil {
			return err
		}
		_, err = exec.RunCommand("ovn-nbctl", "set", "logical_router", gatewayRouter, "options:lb_force_snat_ip="+routerIPByte.String())
		if err != nil {
			return err
		}
		if rampoutIPSubnet != "" {
			rampoutIPSubnets := strings.Split(rampoutIPSubnet, ",")
			for _, rampoutIPSubnet = range rampoutIPSubnets {
				_, _, err = net.ParseCIDR(rampoutIPSubnet)
				if err != nil {
					continue
				}
				// Add source IP address based routes in distributed router
				// for this gateway router
				_, err = exec.RunCommand("ovn-vsctl", "--may-exist", "--policy=src-ip", "lr-route-add", k8sClusterRouter, rampoutIPSubnet, routerIPByte.String())
				if err != nil {
					return err
				}

			}
		}
	}
	return nil
}
