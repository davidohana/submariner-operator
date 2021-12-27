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

package join

import (
	"context"
	"encoding/base64"
	goerrors "errors"
	"fmt"
	"github.com/AlecAivazis/survey/v2"
	"github.com/pkg/errors"
	submariner "github.com/submariner-io/submariner-operator/api/submariner/v1alpha1"
	"github.com/submariner-io/submariner-operator/internal/cli"
	"github.com/submariner-io/submariner-operator/internal/constants"
	"github.com/submariner-io/submariner-operator/internal/image"
	"github.com/submariner-io/submariner-operator/internal/restconfig"
	"github.com/submariner-io/submariner-operator/pkg/broker"
	submarinerclientset "github.com/submariner-io/submariner-operator/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner-operator/pkg/discovery/globalnet"
	"github.com/submariner-io/submariner-operator/pkg/discovery/network"
	"github.com/submariner-io/submariner-operator/pkg/subctl/cmd/utils"
	"github.com/submariner-io/submariner-operator/pkg/subctl/datafile"
	"github.com/submariner-io/submariner-operator/pkg/subctl/operator/brokersecret"
	"github.com/submariner-io/submariner-operator/pkg/subctl/operator/servicediscoverycr"
	"github.com/submariner-io/submariner-operator/pkg/subctl/operator/submarinercr"
	"github.com/submariner-io/submariner-operator/pkg/subctl/operator/submarinerop"
	"github.com/submariner-io/submariner-operator/pkg/version"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"os"
	"regexp"
	"strings"
	"time"
)

type Options struct {
	ClusterID                     string
	ServiceCIDR                   string
	ClusterCIDR                   string
	GlobalnetCIDR                 string
	Repository                    string
	ImageVersion                  string
	NattPort                      int
	IkePort                       int
	PreferredServer               bool
	ForceUDPEncaps                bool
	ColorCodes                    string
	NatTraversal                  bool
	IgnoreRequirements            bool
	GlobalnetEnabled              bool
	IpsecDebug                    bool
	SubmarinerDebug               bool
	OperatorDebug                 bool
	LabelGateway                  bool
	LoadBalancerEnabled           bool
	CableDriver                   string
	GlobalnetClusterSize          uint
	CustomDomains                 []string
	ImageOverrideArr              []string
	HealthCheckEnable             bool
	HealthCheckInterval           uint64
	HealthCheckMaxPacketLossCount uint64
	CorednsCustomConfigMap        string
}

var status = cli.NewStatus()

func SubmarinerCluster(subctlData *datafile.SubctlData, jo Options, restConfigProducer restconfig.Producer) error{
	// Missing information
	qs := []*survey.Question{}

	determineClusterID(jo, restConfigProducer)

	if valid, err := isValidClusterID(jo.ClusterID); !valid {
		fmt.Printf("Error: %s\n", err.Error())

		qs = append(qs, &survey.Question{
			Name:   "clusterID",
			Prompt: &survey.Input{Message: "What is your cluster ID?"},
			Validate: func(val interface{}) error {
				str, ok := val.(string)
				if !ok {
					return nil
				}
				_, err := isValidClusterID(str)
				return err
			},
		})
	}

	if jo.ColorCodes == "" {
		qs = append(qs, &survey.Question{
			Name:     "colorCodes",
			Prompt:   &survey.Input{Message: "What color codes should be used (e.g. \"blue\")?"},
			Validate: survey.Required,
		})
	}

	if len(qs) > 0 {
		answers := struct {
			ClusterID  string
			ColorCodes string
		}{}

		err := survey.Ask(qs, &answers)
		// Most likely a programming error
		utils.PanicOnError(err)

		if len(answers.ClusterID) > 0 {
			jo.ClusterID = answers.ClusterID
		}

		if len(answers.ColorCodes) > 0 {
			jo.ColorCodes = answers.ColorCodes
		}
	}

	clientConfig, err := restConfigProducer.ClientConfig().ClientConfig()
	utils.ExitOnError("Error connecting to the target cluster", err)
	checkRequirements(jo, clientConfig)

	if subctlData.IsConnectivityEnabled() && jo.LabelGateway {
		err := handleNodeLabels(clientConfig)
		utils.ExitOnError("Unable to set the gateway node up", err)
	}

	status.Start("Discovering network details")

	networkDetails := getNetworkDetails(clientConfig)

	status.EndWith(cli.Success)

	serviceCIDR, serviceCIDRautoDetected, err := getServiceCIDR(jo.ServiceCIDR, networkDetails)
	utils.ExitOnError("Error determining the service CIDR", err)

	clusterCIDR, clusterCIDRautoDetected, err := getPodCIDR(jo.ClusterCIDR, networkDetails)
	utils.ExitOnError("Error determining the pod CIDR", err)

	brokerAdminConfig, err := subctlData.GetBrokerAdministratorConfig()
	utils.ExitOnError("Error retrieving broker admin config", err)

	brokerAdminClientset, err := kubernetes.NewForConfig(brokerAdminConfig)
	utils.ExitOnError("Error retrieving broker admin connection", err)

	brokerNamespace := string(subctlData.ClientToken.Data["namespace"])
	netconfig := globalnet.Config{
		ClusterID:               jo.ClusterID,
		GlobalnetCIDR:           jo.GlobalnetCIDR,
		ServiceCIDR:             serviceCIDR,
		ServiceCIDRAutoDetected: serviceCIDRautoDetected,
		ClusterCIDR:             clusterCIDR,
		ClusterCIDRAutoDetected: clusterCIDRautoDetected,
		GlobalnetClusterSize:    jo.GlobalnetClusterSize,
	}

	if jo.GlobalnetEnabled {
		err = AllocateAndUpdateGlobalCIDRConfigMap(jo, brokerAdminClientset, brokerNamespace, &netconfig)
		utils.ExitOnError("Error Discovering multi cluster details", err)
	}

	status.Start("Deploying the Submariner operator")

	operatorImage, err := image.ForOperator(jo.ImageVersion, jo.Repository, jo.ImageOverrideArr)
	utils.ExitOnError("Error overriding Operator Image", err)
	err = submarinerop.Ensure(status, clientConfig, constants.OperatorNamespace, operatorImage, jo.OperatorDebug)
	status.EndWith(cli.CheckForError(err))
	utils.ExitOnError("Error deploying the operator", err)

	status.Start("Creating SA for cluster")

	subctlData.ClientToken, err = broker.CreateSAForCluster(brokerAdminClientset, jo.ClusterID, brokerNamespace)
	status.EndWith(cli.CheckForError(err))
	utils.ExitOnError("Error creating SA for cluster", err)

	// We need to connect to the broker in all cases
	brokerSecret, err := brokersecret.Ensure(clientConfig, constants.OperatorNamespace, populateBrokerSecret(subctlData))
	utils.ExitOnError("Error creating broker secret for cluster", err)

	if subctlData.IsConnectivityEnabled() {
		status.Start("Deploying Submariner")

		err = submarinercr.Ensure(clientConfig, constants.OperatorNamespace, populateSubmarinerSpec(jo, subctlData, brokerSecret, netconfig))
		if err == nil {
			status.QueueSuccessMessage("Submariner is up and running")
			status.EndWith(cli.Success)
		} else {
			status.QueueFailureMessage("Submariner deployment failed")
			status.EndWith(cli.Failure)
		}

		utils.ExitOnError("Error deploying Submariner", err)
	} else if subctlData.IsServiceDiscoveryEnabled() {
		status.Start("Deploying service discovery only")
		err = servicediscoverycr.Ensure(clientConfig, constants.OperatorNamespace, populateServiceDiscoverySpec(jo, subctlData, brokerSecret))
		if err == nil {
			status.QueueSuccessMessage("Service discovery is up and running")
			status.EndWith(cli.Success)
		} else {
			status.QueueFailureMessage("Service discovery deployment failed")
			status.EndWith(cli.Failure)
		}
		utils.ExitOnError("Error deploying service discovery", err)
	}

	return nil
}

func checkRequirements(jo Options, config *rest.Config) {
	_, failedRequirements, err := version.CheckRequirements(config)
	// We display failed requirements even if an error occurred
	if len(failedRequirements) > 0 {
		fmt.Println("The target cluster fails to meet Submariner's requirements:")

		for i := range failedRequirements {
			fmt.Printf("* %s\n", (failedRequirements)[i])
		}

		if !jo.IgnoreRequirements {
			utils.ExitOnError("Unable to check all requirements", err)
			os.Exit(1)
		}
	}

	utils.ExitOnError("Unable to check requirements", err)
}

func determineClusterID(jo Options, restConfigProducer restconfig.Producer) {
	if jo.ClusterID == "" {
		clusterName, err := restConfigProducer.ClusterNameFromContext()
		utils.ExitOnError("Error connecting to the target cluster", err)

		if clusterName != nil {
			jo.ClusterID = *clusterName
		}
	}
}

func AllocateAndUpdateGlobalCIDRConfigMap(jo Options, brokerAdminClientset *kubernetes.Clientset, brokerNamespace string,
	netconfig *globalnet.Config) error {
	status.Start("Discovering multi cluster details")

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		globalnetInfo, globalnetConfigMap, err := globalnet.GetGlobalNetworks(brokerAdminClientset, brokerNamespace)
		if err != nil {
			return errors.Wrap(err, "error reading Global network details on Broker")
		}

		netconfig.GlobalnetCIDR, err = globalnet.ValidateGlobalnetConfiguration(globalnetInfo, *netconfig)
		if err != nil {
			return errors.Wrap(err, "error validating Globalnet configuration")
		}

		if globalnetInfo.Enabled {
			netconfig.GlobalnetCIDR, err = globalnet.AssignGlobalnetIPs(globalnetInfo, *netconfig)
			if err != nil {
				return errors.Wrap(err, "error assigning Globalnet IPs")
			}

			if globalnetInfo.CidrInfo[jo.ClusterID] == nil ||
				globalnetInfo.CidrInfo[jo.ClusterID].GlobalCIDRs[0] != netconfig.GlobalnetCIDR {
				var newClusterInfo broker.ClusterInfo
				newClusterInfo.ClusterID = jo.ClusterID
				newClusterInfo.GlobalCidr = []string{netconfig.GlobalnetCIDR}

				err = broker.UpdateGlobalnetConfigMap(brokerAdminClientset, brokerNamespace, globalnetConfigMap, newClusterInfo)
				return errors.Wrap(err, "error updating Globalnet ConfigMap")
			}
		}

		return nil
	})

	return retryErr // nolint:wrapcheck // No need to wrap here
}

func getNetworkDetails(config *rest.Config) *network.ClusterNetwork {
	dynClient, clientSet, err := restconfig.Clients(config)
	utils.ExitOnError("Unable to set the Kubernetes cluster connection up", err)

	submarinerClient, err := submarinerclientset.NewForConfig(config)
	utils.ExitOnError("Unable to get the Submariner client", err)

	networkDetails, err := network.Discover(dynClient, clientSet, submarinerClient, constants.OperatorNamespace)
	if err != nil {
		status.QueueWarningMessage(fmt.Sprintf("Error trying to discover network details: %s", err))
	} else if networkDetails != nil {
		networkDetails.Show()
	}

	return networkDetails
}

func getPodCIDR(clusterCIDR string, nd *network.ClusterNetwork) (cidrType string, autodetected bool, err error) {
	if clusterCIDR != "" {
		if nd != nil && len(nd.PodCIDRs) > 0 && nd.PodCIDRs[0] != clusterCIDR {
			status.QueueWarningMessage(fmt.Sprintf("Your provided cluster CIDR for the pods (%s) does not match discovered (%s)\n",
				clusterCIDR, nd.PodCIDRs[0]))
		}

		return clusterCIDR, false, nil
	} else if nd != nil && len(nd.PodCIDRs) > 0 {
		return nd.PodCIDRs[0], true, nil
	} else {
		cidrType, err = askForCIDR("Pod")
		return cidrType, false, err
	}
}

func getServiceCIDR(serviceCIDR string, nd *network.ClusterNetwork) (cidrType string, autodetected bool, err error) {
	if serviceCIDR != "" {
		if nd != nil && len(nd.ServiceCIDRs) > 0 && nd.ServiceCIDRs[0] != serviceCIDR {
			status.QueueWarningMessage(fmt.Sprintf("Your provided service CIDR (%s) does not match discovered (%s)\n",
				serviceCIDR, nd.ServiceCIDRs[0]))
		}

		return serviceCIDR, false, nil
	} else if nd != nil && len(nd.ServiceCIDRs) > 0 {
		return nd.ServiceCIDRs[0], true, nil
	} else {
		cidrType, err = askForCIDR("ClusterIP service")
		return cidrType, false, err
	}
}

func askForCIDR(name string) (string, error) {
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

func isValidClusterID(clusterID string) (bool, error) {
	// Make sure the clusterid is a valid DNS-1123 string
	if match, _ := regexp.MatchString("^[a-z0-9][a-z0-9.-]*[a-z0-9]$", clusterID); !match {
		return false, fmt.Errorf("cluster IDs must be valid DNS-1123 names, with only lowercase alphanumerics,\n"+
			"'.' or '-' (and the first and last characters must be alphanumerics).\n"+
			"%s doesn't meet these requirements", clusterID)
	}

	if len(clusterID) > 63 {
		return false, fmt.Errorf("the cluster ID %q has a length of %d characters which exceeds the maximum"+
			" supported length of 63", clusterID, len(clusterID))
	}

	return true, nil
}

func populateBrokerSecret(subctlData *datafile.SubctlData) *v1.Secret {
	// We need to copy the broker token secret as an opaque secret to store it in the connecting cluster
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "broker-secret-",
		},
		Type: v1.SecretTypeOpaque,
		Data: subctlData.ClientToken.Data,
	}
}

func populateSubmarinerSpec(jo Options, subctlData *datafile.SubctlData, brokerSecret *v1.Secret,
	netconfig globalnet.Config) *submariner.SubmarinerSpec {
	brokerURL := subctlData.BrokerURL
	if idx := strings.Index(brokerURL, "://"); idx >= 0 {
		// Submariner doesn't work with a schema prefix
		brokerURL = brokerURL[(idx + 3):]
	}

	// if our network discovery code was capable of discovering those CIDRs
	// we don't need to explicitly set it in the operator
	crServiceCIDR := ""
	if !netconfig.ServiceCIDRAutoDetected {
		crServiceCIDR = netconfig.ServiceCIDR
	}

	crClusterCIDR := ""
	if !netconfig.ClusterCIDRAutoDetected {
		crClusterCIDR = netconfig.ClusterCIDR
	}

	if jo.CustomDomains == nil && subctlData.CustomDomains != nil {
		jo.CustomDomains = *subctlData.CustomDomains
	}

	imageOverrides, err := image.GetOverrides(jo.ImageOverrideArr)
	utils.ExitOnError("Error overriding Operator image", err)

	// For backwards compatibility, the connection information is populated through the secret and individual components
	// TODO skitt This will be removed in the release following 0.12
	submarinerSpec := &submariner.SubmarinerSpec{
		Repository:               getImageRepo(jo),
		Version:                  getImageVersion(jo),
		CeIPSecNATTPort:          jo.NattPort,
		CeIPSecIKEPort:           jo.IkePort,
		CeIPSecDebug:             jo.IpsecDebug,
		CeIPSecForceUDPEncaps:    jo.ForceUDPEncaps,
		CeIPSecPreferredServer:   jo.PreferredServer,
		CeIPSecPSK:               base64.StdEncoding.EncodeToString(subctlData.IPSecPSK.Data["psk"]),
		BrokerK8sCA:              base64.StdEncoding.EncodeToString(brokerSecret.Data["ca.crt"]),
		BrokerK8sRemoteNamespace: string(brokerSecret.Data["namespace"]),
		BrokerK8sApiServerToken:  string(brokerSecret.Data["token"]),
		BrokerK8sApiServer:       brokerURL,
		BrokerK8sSecret:          brokerSecret.ObjectMeta.Name,
		Broker:                   "k8s",
		NatEnabled:               jo.NatTraversal,
		Debug:                    jo.SubmarinerDebug,
		ColorCodes:               jo.ColorCodes,
		ClusterID:                jo.ClusterID,
		ServiceCIDR:              crServiceCIDR,
		ClusterCIDR:              crClusterCIDR,
		Namespace:                constants.SubmarinerNamespace,
		CableDriver:              jo.CableDriver,
		ServiceDiscoveryEnabled:  subctlData.IsServiceDiscoveryEnabled(),
		ImageOverrides:           imageOverrides,
		LoadBalancerEnabled:      jo.LoadBalancerEnabled,
		ConnectionHealthCheck: &submariner.HealthCheckSpec{
			Enabled:            jo.HealthCheckEnable,
			IntervalSeconds:    jo.HealthCheckInterval,
			MaxPacketLossCount: jo.HealthCheckMaxPacketLossCount,
		},
	}
	if netconfig.GlobalnetCIDR != "" {
		submarinerSpec.GlobalCIDR = netconfig.GlobalnetCIDR
	}

	if jo.CorednsCustomConfigMap != "" {
		namespace, name := getCustomCoreDNSParams(jo)
		submarinerSpec.CoreDNSCustomConfig = &submariner.CoreDNSCustomConfig{
			ConfigMapName: name,
			Namespace:     namespace,
		}
	}

	if len(jo.CustomDomains) > 0 {
		submarinerSpec.CustomDomains = jo.CustomDomains
	}

	return submarinerSpec
}

func getImageVersion(jo Options) string {
	if jo.ImageVersion == "" {
		return submariner.DefaultSubmarinerOperatorVersion
	}

	return jo.ImageVersion
}

func getImageRepo(jo Options) string {
	repo := jo.Repository

	if jo.Repository == "" {
		repo = submariner.DefaultRepo
	}

	return repo
}

func removeSchemaPrefix(brokerURL string) string {
	if idx := strings.Index(brokerURL, "://"); idx >= 0 {
		// Submariner doesn't work with a schema prefix
		brokerURL = brokerURL[(idx + 3):]
	}

	return brokerURL
}

func populateServiceDiscoverySpec(jo Options, subctlData *datafile.SubctlData, brokerSecret *v1.Secret) *submariner.ServiceDiscoverySpec {
	brokerURL := removeSchemaPrefix(subctlData.BrokerURL)

	if jo.CustomDomains == nil && subctlData.CustomDomains != nil {
		jo.CustomDomains = *subctlData.CustomDomains
	}

	imageOverrides, err := image.GetOverrides(jo.ImageOverrideArr)
	utils.ExitOnError("Error overriding Operator image", err)

	serviceDiscoverySpec := submariner.ServiceDiscoverySpec{
		Repository:               jo.Repository,
		Version:                  jo.ImageVersion,
		BrokerK8sCA:              base64.StdEncoding.EncodeToString(brokerSecret.Data["ca.crt"]),
		BrokerK8sRemoteNamespace: string(brokerSecret.Data["namespace"]),
		BrokerK8sApiServerToken:  string(brokerSecret.Data["token"]),
		BrokerK8sApiServer:       brokerURL,
		BrokerK8sSecret:          brokerSecret.ObjectMeta.Name,
		Debug:                    jo.SubmarinerDebug,
		ClusterID:                jo.ClusterID,
		Namespace:                constants.SubmarinerNamespace,
		ImageOverrides:           imageOverrides,
	}

	if jo.CorednsCustomConfigMap != "" {
		namespace, name := getCustomCoreDNSParams(jo)
		serviceDiscoverySpec.CoreDNSCustomConfig = &submariner.CoreDNSCustomConfig{
			ConfigMapName: name,
			Namespace:     namespace,
		}
	}

	if len(jo.CustomDomains) > 0 {
		serviceDiscoverySpec.CustomDomains = jo.CustomDomains
	}

	return &serviceDiscoverySpec
}

func isValidCustomCoreDNSConfig(jo Options) error {
	if jo.CorednsCustomConfigMap != "" && strings.Count(jo.CorednsCustomConfigMap, "/") > 1 {
		return fmt.Errorf("coredns-custom-configmap should be in <namespace>/<name> format, namespace is optional")
	}

	return nil
}

func getCustomCoreDNSParams(jo Options) (namespace, name string) {
	if jo.CorednsCustomConfigMap != "" {
		name = jo.CorednsCustomConfigMap

		paramList := strings.Split(jo.CorednsCustomConfigMap, "/")
		if len(paramList) > 1 {
			namespace = paramList[0]
			name = paramList[1]
		}
	}

	return namespace, name
}

func handleNodeLabels(config *rest.Config) error {
	_, clientset, err := restconfig.Clients(config)
	utils.ExitOnError("Unable to set the Kubernetes cluster connection up", err)
	// List Submariner-labeled nodes
	const submarinerGatewayLabel = "submariner.io/gateway"
	const trueLabel = "true"

	selector := labels.SelectorFromSet(map[string]string{submarinerGatewayLabel: trueLabel})

	labeledNodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return errors.Wrap(err, "error listing Nodes")
	}

	if len(labeledNodes.Items) > 0 {
		fmt.Printf("* There are %d labeled nodes in the cluster:\n", len(labeledNodes.Items))

		for i := range labeledNodes.Items {
			fmt.Printf("  - %s\n", labeledNodes.Items[i].GetName())
		}
	} else {
		answer, err := askForGatewayNode(clientset)
		if err != nil {
			return err
		}

		if answer.Node == "" {
			fmt.Printf("* No worker node found to label as the gateway\n")
		} else {
			err = addLabelsToNode(clientset, answer.Node, map[string]string{submarinerGatewayLabel: trueLabel})
			utils.ExitOnError("Error labeling the gateway node", err)
		}
	}

	return nil
}

func askForGatewayNode(clientset kubernetes.Interface) (struct{ Node string }, error) {
	// List the worker nodes and select one
	workerNodes, err := clientset.CoreV1().Nodes().List(
		context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker"})
	if err != nil {
		return struct{ Node string }{}, errors.Wrap(err, "error listing Nodes")
	}

	if len(workerNodes.Items) == 0 {
		// In some deployments (like KIND), worker nodes are not explicitly labelled. So list non-master nodes.
		workerNodes, err = clientset.CoreV1().Nodes().List(
			context.TODO(), metav1.ListOptions{LabelSelector: "!node-role.kubernetes.io/master"})
		if err != nil {
			return struct{ Node string }{}, errors.Wrap(err, "error listing Nodes")
		}

		if len(workerNodes.Items) == 0 {
			return struct{ Node string }{}, nil
		}
	}

	if len(workerNodes.Items) == 1 {
		return struct{ Node string }{workerNodes.Items[0].GetName()}, nil
	}

	allNodeNames := []string{}
	for i := range workerNodes.Items {
		allNodeNames = append(allNodeNames, workerNodes.Items[i].GetName())
	}

	qs := []*survey.Question{
		{
			Name: "node",
			Prompt: &survey.Select{
				Message: "Which node should be used as the gateway?",
				Options: allNodeNames,
			},
		},
	}

	answers := struct {
		Node string
	}{}

	err = survey.Ask(qs, &answers)
	if err != nil {
		return struct{ Node string }{}, err // nolint:wrapcheck // No need to wrap here
	}

	return answers, nil
}

// this function was sourced from:
// https://github.com/kubernetes/kubernetes/blob/a3ccea9d8743f2ff82e41b6c2af6dc2c41dc7b10/test/utils/density_utils.go#L36
func addLabelsToNode(c kubernetes.Interface, nodeName string, labelsToAdd map[string]string) error {
	tokens := make([]string, 0, len(labelsToAdd))
	for k, v := range labelsToAdd {
		tokens = append(tokens, fmt.Sprintf("%q:%q", k, v))
	}

	labelString := "{" + strings.Join(tokens, ",") + "}"
	patch := fmt.Sprintf(`{"metadata":{"labels":%v}}`, labelString)

	// retry is necessary because nodes get updated every 10 seconds, and a patch can happen
	// in the middle of an update

	var lastErr error
	err := wait.ExponentialBackoff(nodeLabelBackoff, func() (bool, error) {
		_, lastErr = c.CoreV1().Nodes().Patch(context.TODO(), nodeName, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
		if lastErr != nil {
			if !k8serrors.IsConflict(lastErr) {
				return false, lastErr // nolint:wrapcheck // No need to wrap here
			}
			return false, nil
		}

		return true, nil
	})

	if goerrors.Is(err, wait.ErrWaitTimeout) {
		return lastErr // nolint:wrapcheck // No need to wrap here
	}

	return err // nolint:wrapcheck // No need to wrap here
}

var nodeLabelBackoff wait.Backoff = wait.Backoff{
	Steps:    10,
	Duration: 1 * time.Second,
	Factor:   1.2,
	Jitter:   1,
}