package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/containernetworking/cni/pkg/ip"
	"github.com/containernetworking/cni/pkg/ipam"
	"github.com/containernetworking/cni/pkg/ns"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	//"github.com/containernetworking/cni/pkg/version"
	"github.com/mozhuli/ovn-stackube/pkg/common"
	"github.com/mozhuli/ovn-stackube/pkg/exec"
	"github.com/vishvananda/netlink"
)

// NetConf stores the common network config for ovn CNI plugin
type NetConf struct {
	Bridge string `json:"bridge"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	IPAM   struct {
		Type   string `json:"type"`
		Subnet string `json:"subnet"`
	} `json:"ipam,omitempty"`
	IPMasq    bool   `json:"ipMasq"`
	IsGateway bool   `json:"isGateway"`
	LogLevel  string `json:"log_level"`
}

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

func setupInterface(containerId string, netns ns.NetNS, ifName string, macAddress string) (string, error) {
	var hostVethName string

	err := netns.Do(func(hostNS ns.NetNS) error {
		// create the veth pair in the container and move host end into host netns
		hostVeth, contVeth, err := ip.SetupVeth(ifName, mtu, hostNS)
		if err != nil {
			return err
		}
		hw, err := net.ParseMAC(macAddress)
		if err != nil {
			return err
		}
		err = netlink.LinkSetHardwareAddr(contVeth, hw)
		if err != nil {
			return err
		}

		hostVethName = hostVeth.Attrs().Name

		return nil
	})
	if err != nil {
		return "", err
	}

	// need to lookup hostVeth again as its index has changed during ns move
	hostVeth, err := netlink.LinkByName(hostVethName)
	if err != nil {
		return "", fmt.Errorf("failed to lookup %q: %v", hostVethName, err)
	}

	// set hairpin mode
	if err = netlink.LinkSetHairpin(hostVeth, hairpinMode); err != nil {
		return "", fmt.Errorf("failed to setup hairpin mode for %v: %v", hostVethName, err)
	}
	if err = netlink.LinkSetName(hostVeth, containerId[:15]); err != nil {
		return "", err
	}

	return containerId[:15], nil

}

func cmdAdd(args *skel.CmdArgs) error {
	conf := NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to load netconf: %v", err)
	}

	re, err := exec.RunCommand("--if-exists", "get", "Open_vSwitch", ".", "external_ids:k8s-api-server")
	if err != nil || re == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed get k8sApiServer")
	}
	k8sApiServer := strings.Trim(re[0], "\"")
	if !strings.HasPrefix(k8sApiServer, "http") {
		k8sApiServer = "http://" + k8sApiServer
	}

	cniArgsMap := make(map[string]string)
	for _, cniArg := range strings.Split(args.Args, ";") {
		cniArgsMap[strings.Split(cniArg, "=")[0]] = strings.Split(cniArg, "=")[1]
	}
	namespace, ok := cniArgsMap["K8S_POD_NAMESPACE"]
	if !ok {
		return fmt.Errorf("there is no key K8S_POD_NAMESPACE")
	}
	podName, ok := cniArgsMap["K8S_POD_NAME"]
	if !ok {
		return fmt.Errorf("there is no key K8S_POD_NAME")
	}
	containerId, ok := cniArgsMap["K8S_POD_INFRA_CONTAINER_ID"]
	if !ok {
		return fmt.Errorf("there is no key K8S_POD_INFRA_CONTAINER_ID")
	}

	counter := 30
	var annotations map[string]interface{}
	for ; counter > 0; counter-- {
		annotations, err := common.GetPodAnnotations(k8sApiServer, namespace, podName)
		if err != nil {
			return fmt.Errorf("failed to get pod annotation: %v", err)
		}
		if annotations != nil {
			if ovn, ok := annotations["ovn"]; ok {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	ovn := annotations["ovn"].(map[string]string)
	macAddress, ok := ovn["mac_address"]
	if !ok {
		return fmt.Errorf("missing key macAddress")
	}
	ipAddress, ok := ovn["ip_address"]
	if !ok {
		return fmt.Errorf("missing key ipAddress")
	}
	gatewayIp, ok := ovn["gateway_ip"]
	if !ok {
		return fmt.Errorf("missing key gatewayIp")
	}
	var result *types.Result
	ipc, ipnet, _ := net.ParseCIDR(ipAddress)
	ipg, _, err := net.ParseCIDR(gatewayIp)

	ipnet.IP = ipc
	result.IP4.IP = *ipnet
	result.IP4.Gateway = ipg

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	vethOutside, err := setupInterface(containerId, netns, args.IfName, macAddress)
	if err != nil {
		return err
	}
	if err := netns.Do(func(_ ns.NetNS) error {
		// set the default gateway if requested
		if n.IsDefaultGW {
			_, defaultNet, err := net.ParseCIDR("0.0.0.0/0")
			if err != nil {
				return err
			}

			for _, route := range result.IP4.Routes {
				if defaultNet.String() == route.Dst.String() {
					if route.GW != nil && !route.GW.Equal(result.IP4.Gateway) {
						return fmt.Errorf(
							"isDefaultGateway ineffective because IPAM sets default route via %q",
							route.GW,
						)
					}
				}
			}

			result.IP4.Routes = append(
				result.IP4.Routes,
				types.Route{Dst: *defaultNet, GW: result.IP4.Gateway},
			)

			// TODO: IPV6
		}

		return ipam.ConfigureIface(args.IfName, result)
	}); err != nil {
		return err
	}

	ifaceId := namespace + "_" + podName

	_, err = exec.RunCommand("ovs-vsctl", "add-port", "br-int", vethOutside, "--", "set", "interface", vethOutside, "external_ids:attached_mac="+macAddress, "external_ids:iface-id="+ifaceId, "external_ids:ip_address="+ipAddress)
	if err != nil {
		return fmt.Errorf("Unable to plug interface into OVN bridge: %v", err)
	}
	return nil
}

func cmdDel(args *skel.CmdArgs) error {
	conf := NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to load netconf: %v", err)
	}

	if args.Netns != "" {
		fmt.Fprintf(os.Stderr, "Calico CNI deleting device in netns %s\n", args.Netns)
		err := ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {
			_, err := ip.DelLinkByNameAddr(args.IfName, netlink.FAMILY_V4)
			return err
		})

		if err != nil {
			return err
		}
	}
	_, err := exec.RunCommand("ovs-vsctl", "del-port", args.ContainerID[:15])
	if err != nil {
		return err
	}
	_, err = exec.RunCommand("rm", "-f", "/var/run/netns/"+args.ContainerID[:15])
	if err != nil {
		return err
	}
	return nil
}

// VERSION is filled out during the build process (using git describe output)
var VERSION string

func main() {
	// Display the version on "-v", otherwise just delegate to the skel code.
	// Use a new flag set so as not to conflict with existing libraries which use "flag"
	flagSet := flag.NewFlagSet("ovn-cni", flag.ExitOnError)

	version := flagSet.Bool("v", false, "Display version")
	err := flagSet.Parse(os.Args[1:])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if *version {
		fmt.Println(VERSION)
		os.Exit(0)
	}
	skel.PluginMain(cmdAdd, cmdDel)
}
