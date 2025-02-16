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

package deployment

import (
	"github.com/submariner-io/submariner-operator/pkg/names"
	"github.com/submariner-io/submariner-operator/pkg/subctl/operator/common/operatorpod"
	"k8s.io/client-go/kubernetes"
)

// Ensure the operator is deployed, and running.
func Ensure(kubeClient kubernetes.Interface, namespace, image string, debug bool) (bool, error) {
	return operatorpod.Ensure(kubeClient, namespace, names.OperatorComponent, image, debug) // nolint:wrapcheck // No need to wrap here
}
