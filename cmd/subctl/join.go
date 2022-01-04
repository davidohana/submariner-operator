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
	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
	submariner "github.com/submariner-io/submariner-operator/api/submariner/v1alpha1"
	"github.com/submariner-io/submariner-operator/internal/cli"
	"github.com/submariner-io/submariner-operator/internal/exit"
	"github.com/submariner-io/submariner-operator/internal/restconfig"
	"github.com/submariner-io/submariner-operator/pkg/broker"
	"github.com/submariner-io/submariner-operator/pkg/join"
	"github.com/submariner-io/submariner-operator/pkg/subctl/datafile"
	"strings"
)

var joinFlags join.Options

var joinCmd = &cobra.Command{
	Use:     "join",
	Short:   "Connect a cluster to an existing broker",
	Args:    cobra.MaximumNArgs(1),
	PreRunE: restConfigProducer.CheckVersionMismatch,
	Run: func(cmd *cobra.Command, args []string) {
		err := checkArgumentPassed(args)
		exit.OnError(err)

		subctlData, err := datafile.NewFromFile(args[0])
		exit.WithMessage("Error loading the broker information from the given file", err)
		fmt.Printf("* %s says broker is at: %s\n", args[0], subctlData.BrokerURL)

		if joinFlags.ClusterID == "" {
			joinFlags.ClusterID, err = askForClusterID()
			exit.WithMessage("Error collecting information", err)
		}

		if joinFlags.ServiceCIDR == "" {
			joinFlags.ServiceCIDR, err = askForCIDR("Service")
			exit.WithMessage("Error detecting Service CIDR", err)
		}

		if joinFlags.ClusterCIDR == "" {
			joinFlags.ClusterCIDR, err = askForCIDR("Cluster")
			exit.WithMessage("Error detecting Cluster CIDR", err)
		}

		clientConfig, err := restConfigProducer.ClientConfig().ClientConfig()
		_, clientset, err := restconfig.Clients(clientConfig)
		exit.WithMessage("unable to set the Kubernetes cluster connection up", err)

		gatewayNodesPresent, err := join.ListGatewayNodes(clientConfig)
		if err != nil {
			exit.WithMessage("Error getting gateway node", err)
		}
		var gatewayNode struct{Node string}
		// If not Gateway nodes present, get all worker nodes and ask user to select one of them as gateway node
		if !gatewayNodesPresent {
			allWorkerNodeNames, err := join.GetAllWorkerNodeNames(clientset)
			if err != nil {
				exit.WithMessage("error listing worker nodes", err)
			}
			gatewayNode, err = askForGatewayNode(allWorkerNodeNames)
			exit.WithMessage("Error getting gateway node", err)
		}

		brokerInfo, err := broker.ReadInfoFromFile(broker.InfoFileName)

		err = join.SubmarinerCluster(*brokerInfo, joinFlags, restConfigProducer, cli.NewReporter(), gatewayNode)
		exit.WithMessage("Error joining cluster", err)
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

func askForGatewayNode(allWorkerNodeNames []string) (struct{ Node string }, error) {
	qs := []*survey.Question{
		{
			Name: "node",
			Prompt: &survey.Select{
				Message: "Which node should be used as the gateway?",
				Options: allWorkerNodeNames,
			},
		},
	}

	answers := struct {
		Node string
	}{}

	err := survey.Ask(qs, &answers)
	if err != nil {
		return struct{ Node string }{}, err // nolint:wrapcheck // No need to wrap here
	}

	return answers, nil
}

func checkArgumentPassed(args []string) error {
	if len(args) == 0 {
		return errors.New("broker-info.subm file generated by 'subctl deploy-broker' not passed")
	}

	return nil
}

func askForClusterID() (string, error) {
	// Missing information
	qs := []*survey.Question{}

	qs = append(qs, &survey.Question{
		Name:   "clusterID",
		Prompt: &survey.Input{Message: "What is your cluster ID?"},
		Validate: func(val interface{}) error {
			str, ok := val.(string)
			if !ok {
				return nil
			}

			_, err := join.IsValidClusterID(str)
			fmt.Printf("error is %s", err)
			return err
		},
	})

	answers := struct {
		ClusterID  string
	}{}

	err := survey.Ask(qs, &answers)
	// Most likely a programming error
	if err != nil {
		return "", err
	}

	return answers.ClusterID, nil
}

func askForCIDR(name string) (string, error) {
	autodetect := false
	prompt := &survey.Confirm{
		Message: fmt.Sprintf("Do you want to autodetect %s CIDR?", name)}

	err := survey.AskOne(prompt, &autodetect)
	if err != nil {
		return "", err // nolint:wrapcheck // No need to wrap here
	}

	if !autodetect {
		qs := []*survey.Question{{
			Name:     "cidr",
			Prompt:   &survey.Input{Message: fmt.Sprintf("What's the %s CIDR for your cluster?", name)},
			Validate: survey.Required,
		}}

		answers := struct {
			Cidr string
		}{}

		err := survey.Ask(qs, &answers)
		if err != nil {
			return "", err // nolint:wrapcheck // No need to wrap here
		}

		return strings.TrimSpace(answers.Cidr), nil
	}
	fmt.Printf("Autodetecting %s CIDR", name)
	return "", nil
}
