package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/mozhuli/ovn-stackube/pkg/common"
	"github.com/mozhuli/ovn-stackube/pkg/exec"
)

var CNI_CONF_PATH = "/etc/cni/net.d"
var CNI_LINK_PATH = "/opt/cni/bin"
var CNI_PLUGIN = "ovn-k8s-cni-overlay"
var OVN_NB string
var K8S_API_SERVER string
var K8S_CLUSTER_ROUTER string
var K8S_CLUSTER_LB_TCP string
var K8S_CLUSTER_LB_UDP string
var K8S_NS_LB_TCP string
var K8S_NS_LB_UDP string
var OVN_MODE string

func fetchOVNNB() (string, error) {
	re, err := exec.RunCommand("ovs-vsctl", "--if-exists", "get", "Open_vSwitch", ".", "external_ids:ovn-nb")
	if err != nil || re == nil {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("failed get OVN central database's ip address")
	}
	OVN_NB := strings.Trim(re[0], "\"")
	if OVN_NB == "" {
		return "", fmt.Errorf("OVN central database's ip address not set")
	}
	return OVN_NB, nil
}

func getK8sClusterRouter() (string, error) {
	re, err := exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=_uuid", "find", "logical_router", "external_ids:k8s-cluster-router=yes")
	if err != nil || re == nil {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("failed get k8sClusterRouter")
	}
	return re[0], nil
}

func getLocalSystemID() (string, error) {
	re, err := exec.RunCommand("ovs-vsctl", "--if-exists", "get", "Open_vSwitch", ".", "external_ids:system-id")
	if err != nil || re == nil {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("failed get k8sClusterRouter")
	}
	return strings.Trim(re[0], "\""), nil
}

func configureManagementPortDebian(nodeName, clusterSubnet, routerIP, interfaceName, interfaceIP string) error {
	bridgeExists := false
	interfaceExists := false
	bridgeTemplate := "allow-ovs br-int\niface br-int inet manual\n\tovs_type OVSBridge\n\tovs_ports " + interfaceName + "\n\tovs_extra set bridge br-int fail_mode=secure\n"
	ip, interfaceIPNet, _ := net.ParseCIDR(interfaceIP)
	_, clusterIPNet, _ := net.ParseCIDR(clusterSubnet)

	interfaceTemplate := "allow-br-int " + interfaceName + "\niface " + interfaceName + " inet static\n\taddress " + ip.String() + "\n\tnetmask " +
		interfaceIPNet.Mask.String() + "\n\tovs_type OVSIntPort\n\tovs_bridge br-int\n\tovs_extra set interface $IFACE external-ids:iface-id=k8s-" +
		nodeName + "\n\tup route add -net " + clusterIPNet.IP.String() + " netmask " + clusterIPNet.Mask.String() + " gw " + routerIP + "\n\tdown route del -net " +
		clusterIPNet.IP.String() + " netmask " + clusterIPNet.Mask.String() + " gw " + routerIP + "\n"

	f, err := os.Open("/etc/network/interfaces")
	if err == nil {
		return fmt.Errorf("failed open file /etc/network/interfaces: %v", err)
	}
	defer f.Close()
	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadString('\n')
		if err != nil || io.EOF == err {
			break
		}
		// Look for a line of the form "allow-ovs br-int"
		if strings.Contains(line, "allow-ovs") && strings.Contains(line, "br-int") {
			bridgeExists = true
			break
		}
		// Look for a line of the form "allow-br-int $interfaceName"
		if strings.Contains(line, "allow-br-int") && strings.Contains(line, interfaceName) {
			interfaceExists = true
			break
		}
	}
	if !bridgeExists {
		f, err := os.OpenFile("/etc/network/interfaces", os.O_WRONLY, 0644)
		if err != nil {
			return err
		} else {
			n, _ := f.Seek(0, os.SEEK_END)
			_, err = f.WriteAt([]byte(bridgeTemplate), n)
			if err != nil {
				return err
			}
		}
		defer f.Close()
	}
	if !interfaceExists {
		f, err := os.OpenFile("/etc/network/interfaces", os.O_WRONLY, 0644)
		if err != nil {
			return err
		} else {
			n, _ := f.Seek(0, os.SEEK_END)
			_, err = f.WriteAt([]byte(interfaceTemplate), n)
			if err != nil {
				return err
			}
		}
		defer f.Close()
	}
	return nil
}

func configureManagementPortRedhat(nodeName, clusterSubnet, routerIP, interfaceName, interfaceIP string) error {
	//TODO
	return nil
}

func configureManagementPort(nodeName, clusterSubnet, routerIP, interfaceName, interfaceIP string) error {
	// First, try to configure management ports via platform specific tools.
	// Identify whether the platform is Debian based.
	_, err := os.Stat("/etc/network/interfaces")
	if err == nil {
		err := configureManagementPortDebian(nodeName, clusterSubnet, routerIP, interfaceName, interfaceIP)
		if err != nil {
			return err
		}
	}
	_, err = os.Stat("/etc/sysconfig/network-scripts/ifup-ovs")
	if err == nil {
		err := configureManagementPortRedhat(nodeName, clusterSubnet, routerIP, interfaceName, interfaceIP)
		if err != nil {
			return err
		}
	}
	// Up the interface.
	_, err = exec.RunCommand("ip", "link", "set", interfaceName, "up")
	if err != nil {
		return err
	}
	// The interface may already exist, in which case delete the routes and IP.
	_, err = exec.RunCommand("ip", "addr", "flush", "dev", interfaceName)
	if err != nil {
		return err
	}
	// Assign IP address to the internal interface.
	_, err = exec.RunCommand("ip", "addr", "add", interfaceIP, "dev", interfaceName)
	if err != nil {
		return err
	}
	// Flush the route for the entire subnet (in case it was added before)
	_, err = exec.RunCommand("ip", "route", "flush", clusterSubnet)
	if err != nil {
		return err
	}
	// Create a route for the entire subnet.
	_, err = exec.RunCommand("ip", "route", "add", clusterSubnet, "via", routerIP)
	if err != nil {
		return err
	}
	return nil
}

// Create a logical switch for the node and connect it to the distributed router.  This switch will start with one logical port (A OVS internal interface).
// 1.  This logical port is via which a node can access all other nodes and the containers running inside them using the private IP addresses.
// 2.  When this port is created on the master node, the K8s daemons become reachable from the containers without any NAT.
// 3.  The nodes can health-check the pod IP addresses.
func createManagementPort(nodeName, localSubnet, clusterSubnet string) error {
	// Create a router port and provide it the first address in the 'local_subnet'.
	ip, localSubnetNet, err := net.ParseCIDR(localSubnet)
	if err != nil {
		return fmt.Errorf("failed parse localsubnet v% : %v", localSubnetNet, err)
	}
	ip = common.NextIP(ip)
	n, _ := localSubnetNet.Mask.Size()
	routerIPMask := fmt.Sprintf("%s\\%d", ip.String(), n)
	routerIP := ip.String()

	re, err := exec.RunCommand("ovn-nbctl", "--if-exist", "get", "logical_router_port", "rtos-"+nodeName, "mac")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed get routerMac")
	}
	routerMac := strings.Trim(re[0], "\"")
	if routerMac == "" {
		routerMac = common.GenerateMac()
		clusterRouter, err := getK8sClusterRouter()
		if err != nil {
			return err
		}
		_, err = exec.RunCommand("ovn-nbctl", "--may-exist", "lrp-add", clusterRouter, "rtos-"+nodeName, routerMac, routerIPMask)
		if err != nil {
			return err
		}
	}
	// Create a logical switch and set its subnet.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "ls-add", nodeName, "--", "set", "logical_switch", nodeName, "other-config:subnet="+localSubnet, "external-ids:gateway_ip="+routerIPMask)
	if err != nil {
		return err
	}
	// Connect the switch to the router.
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lsp-add", nodeName, "stor-"+nodeName, "--", "set", "logical_switch_port", "stor-"+nodeName, "type=router", "options:router-port=rtos-"+nodeName, "addresses="+"\""+routerMac+"\"")
	if err != nil {
		return err
	}
	interfaceName := "k8s-" + (nodeName[:11])
	// Create a OVS internal interface
	_, err = exec.RunCommand("ovs-vsctl", "--", "--may-exist", "add-port", "br-int", interfaceName, "--", "set", "interface", interfaceName, "type=internal", "mtu_request=1400", "external-ids:iface-id=k8s-"+nodeName)
	if err != nil {
		return err
	}
	re, err = exec.RunCommand("ovs-vsctl", "--if-exists", "get", "interface", interfaceName, "mac_in_use")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get mac address of ovn-k8s-master")
	}
	macAddress := strings.Trim(re[0], "\"")
	// Create the OVN logical port.
	ip = common.NextIP(ip)
	portIP := ip.String()
	portIPMask := fmt.Sprintf("%s\\%d", portIP, n)
	_, err = exec.RunCommand("ovn-nbctl", "--", "--may-exist", "lsp-add", nodeName, "k8s-"+nodeName, "--", "lsp-set-addresses", "k8s-"+nodeName, macAddress+" "+portIP)
	if err != nil {
		return err
	}
	err = configureManagementPort(nodeName, clusterSubnet, routerIP, interfaceName, portIPMask)
	if err != nil {
		return err
	}
	// Add the load_balancer to the switch.
	re, err = exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=_uuid", "find", "load_balancer", "external_ids:k8s-cluster-lb-tcp=yes")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get k8sClusterLbTcp")
	}
	k8sClusterLbTcp := re[0]
	if k8sClusterLbTcp != "" {
		_, err = exec.RunCommand("ovn-nbctl", "set", "logical_switch", nodeName, "load_balancer="+k8sClusterLbTcp)
		if err != nil {
			return err
		}
	}

	re, err = exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=_uuid", "find", "load_balancer", "external_ids:k8s-cluster-lb-udp=yes")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get k8sClusterLbUdp")
	}
	k8sClusterLbUdp := re[0]
	if k8sClusterLbUdp != "" {
		_, err = exec.RunCommand("ovn-nbctl", "add", "logical_switch", nodeName, "load_balancer="+k8sClusterLbUdp)
		if err != nil {
			return err
		}
	}
	return nil
}

func generateGatewayIP() (string, error) {
	// All the routers connected to "join" switch are in 100.64.1.0/24
	// network and they have their external_ids:connect_to_join set.
	re, err := exec.RunCommand("ovn-nbctl", "--data=bare", "--no-heading", "--columns=network", "find", "logical_router_port", "external_ids:connect_to_join=yes")
	if err != nil {
		return "", err
	}

	ipStart, ipStartNet, _ := net.ParseCIDR("100.64.1.0/24")
	ipMax, _, _ := net.ParseCIDR("100.64.1.255/24")
	n, _ := ipStartNet.Mask.Size()
	for !ipStart.Equal(ipMax) {
		ipStart = common.NextIP(ipStart)
		used := 0
		for _, v := range re {
			if ipStart.String() == v {
				used = 1
				break
			}
		}
		if used == 1 {
			continue
		} else {
			break
		}
	}
	ipMask := fmt.Sprintf("%s\\%d", ipStart.String(), n)
	return ipMask, nil
}
