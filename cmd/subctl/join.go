/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package subctl

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	submariner "github.com/submariner-io/submariner-operator/api/submariner/v1alpha1"
	"github.com/submariner-io/submariner-operator/pkg/join"
	"github.com/submariner-io/submariner-operator/pkg/subctl/cmd/utils"
	"github.com/submariner-io/submariner-operator/pkg/subctl/datafile"
)

var joinFlags join.Options

var joinCmd = &cobra.Command{
	Use:     "join",
	Short:   "Connect a cluster to an existing broker",
	Args:    cobra.MaximumNArgs(1),
	PreRunE: restConfigProducer.CheckVersionMismatch,
	Run: func(cmd *cobra.Command, args []string) {
		err := checkArgumentPassed(args)
		utils.ExitOnError("Argument missing", err)
		subctlData, err := datafile.NewFromFile(args[0])
		utils.ExitOnError("Argument missing", err)
		utils.ExitOnError("Error loading the broker information from the given file", err)
		fmt.Printf("* %s says broker is at: %s\n", args[0], subctlData.BrokerURL)
		utils.ExitOnError("Error connecting to broker cluster", err)
		err = isValidCustomCoreDNSConfig()
		utils.ExitOnError("Invalid Custom CoreDNS configuration", err)
		join.SubmarinerCluster(subctlData, joinFlags)
	},
}

func init() {
	addJoinFlags(joinCmd)
	restConfigProducer.AddKubeContextFlag(joinCmd)
	rootCmd.AddCommand(joinCmd)
}

func addJoinFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&joinFlags.ClusterID, "clusterid", "", "cluster ID used to identify the tunnels")
	cmd.Flags().StringVar(&joinFlags.ServiceCIDR, "servicecidr", "", "service CIDR")
	cmd.Flags().StringVar(&joinFlags.ClusterCIDR, "clustercidr", "", "cluster CIDR")
	cmd.Flags().StringVar(&joinFlags.Repository, "repository", "", "image repository")
	cmd.Flags().StringVar(&joinFlags.ImageVersion, "version", "", "image version")
	cmd.Flags().StringVar(&joinFlags.ColorCodes, "colorcodes", submariner.DefaultColorCode, "color codes")
	cmd.Flags().IntVar(&joinFlags.NattPort, "nattport", 4500, "IPsec NATT port")
	cmd.Flags().IntVar(&joinFlags.IkePort, "ikeport", 500, "IPsec IKE port")
	cmd.Flags().BoolVar(&joinFlags.NatTraversal, "natt", true, "enable NAT traversal for IPsec")

	cmd.Flags().BoolVar(&joinFlags.PreferredServer, "preferred-server", false,
		"enable this cluster as a preferred server for dataplane connections")

	cmd.Flags().BoolVar(&joinFlags.LoadBalancerEnabled, "load-balancer", false,
		"enable automatic LoadBalancer in front of the gateways")

	cmd.Flags().BoolVar(&joinFlags.ForceUDPEncaps, "force-udp-encaps", false, "force UDP encapsulation for IPSec")

	cmd.Flags().BoolVar(&joinFlags.IpsecDebug, "ipsec-debug", false, "enable IPsec debugging (verbose logging)")
	cmd.Flags().BoolVar(&joinFlags.SubmarinerDebug, "pod-debug", false,
		"enable Submariner pod debugging (verbose logging in the deployed pods)")
	cmd.Flags().BoolVar(&joinFlags.OperatorDebug, "operator-debug", false, "enable operator debugging (verbose logging)")
	cmd.Flags().BoolVar(&joinFlags.LabelGateway, "label-gateway", true, "label gateways if necessary")
	cmd.Flags().StringVar(&joinFlags.CableDriver, "cable-driver", "", "cable driver implementation")
	cmd.Flags().UintVar(&joinFlags.GlobalnetClusterSize, "globalnet-cluster-size", 0,
		"cluster size for GlobalCIDR allocated to this cluster (amount of global IPs)")
	cmd.Flags().StringVar(&joinFlags.GlobalnetCIDR, "globalnet-cidr", "",
		"GlobalCIDR to be allocated to the cluster")
	cmd.Flags().StringSliceVar(&joinFlags.CustomDomains, "custom-domains", nil,
		"list of domains to use for multicluster service discovery")
	cmd.Flags().StringSliceVar(&joinFlags.ImageOverrideArr, "image-override", nil,
		"override component image")
	cmd.Flags().BoolVar(&joinFlags.HealthCheckEnable, "health-check", true,
		"enable Gateway health check")
	cmd.Flags().Uint64Var(&joinFlags.HealthCheckInterval, "health-check-interval", 1,
		"interval in seconds between health check packets")
	cmd.Flags().Uint64Var(&joinFlags.HealthCheckMaxPacketLossCount, "health-check-max-packet-loss-count", 5,
		"maximum number of packets lost before the connection is marked as down")
	cmd.Flags().BoolVar(&joinFlags.GlobalnetEnabled, "globalnet", true,
		"enable/disable Globalnet for this cluster")
	cmd.Flags().StringVar(&joinFlags.CorednsCustomConfigMap, "coredns-custom-configmap", "",
		"Name of the custom CoreDNS configmap to configure forwarding to lighthouse. It should be in "+
			"<namespace>/<name> format where <namespace> is optional and defaults to kube-system")
	cmd.Flags().BoolVar(&joinFlags.IgnoreRequirements, "ignore-requirements", false, "ignore requirement failures (unsupported)")
}

