/*
Copyright 2018 The Knative Authors

Licensed under the Apache License, Open 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ingress

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type IngressServices struct {
	coreClient kubernetes.Interface
}

type IngressService interface {
	Name() string
	Addresses() []string
	Ports() []int32
	MappedPort(int32) int32
	CreationTime() time.Time
}

type IngressServiceLoadBalancer struct {
	corev1.Service
}

var _ IngressService = IngressServiceLoadBalancer{}

type IngressServiceNodePort struct {
	coreClient kubernetes.Interface
	corev1.Service
}

var _ IngressService = IngressServiceNodePort{}

func NewIngressServices(coreClient kubernetes.Interface) IngressServices {
	return IngressServices{coreClient}
}

func (s IngressServices) List() ([]IngressService, error) {
	listOpts := metav1.ListOptions{
		LabelSelector: labels.Set(map[string]string{
			"knative": "ingressgateway",
		}).String(),
	}

	istioNsName := NewIstio().SystemNamespaceName()

	services, err := s.coreClient.CoreV1().Services(istioNsName).List(listOpts)
	if err != nil {
		return nil, fmt.Errorf("Listing services in istio namespace: %s", err)
	}

	var ingSvcs []IngressService

	for _, svc := range services.Items {
		switch svc.Spec.Type {
		case corev1.ServiceTypeLoadBalancer:
			ingSvcs = append(ingSvcs, IngressServiceLoadBalancer{svc})

		case corev1.ServiceTypeNodePort:
			ingSvcs = append(ingSvcs, IngressServiceNodePort{s.coreClient, svc})

		case corev1.ServiceTypeClusterIP, corev1.ServiceTypeExternalName:
			// TODO ing service
		}
	}

	return ingSvcs, nil
}

func (s IngressServices) PreferredAddress(port int32) (string, string, error) {
	ingSvcs, err := s.List()
	if err != nil {
		return "", "", err
	}

	for _, svc := range ingSvcs {
		addrs := svc.Addresses()
		port = svc.MappedPort(port)

		if len(addrs) > 0 && port != 0 {
			return addrs[0], fmt.Sprintf("%d", port), nil
		}
	}

	return "", "", fmt.Errorf("Expected to find at least one ingress address")
}

func (s IngressServiceLoadBalancer) Name() string { return s.Service.Name }

func (s IngressServiceLoadBalancer) CreationTime() time.Time {
	return s.CreationTimestamp.Time
}

func (s IngressServiceLoadBalancer) Addresses() []string {
	addrs := []string{}

	for _, ing := range s.Status.LoadBalancer.Ingress {
		if len(ing.IP) > 0 {
			addrs = append(addrs, ing.IP)
		}
		if len(ing.Hostname) > 0 {
			addrs = append(addrs, ing.Hostname)
		}
	}

	return addrs
}

func (s IngressServiceLoadBalancer) Ports() []int32 {
	ports := []int32{}

	for _, port := range s.Spec.Ports {
		ports = append(ports, port.Port)
	}

	return ports
}

func (s IngressServiceLoadBalancer) MappedPort(port int32) int32 {
	for _, p := range s.Spec.Ports {
		if p.Port == port {
			return port
		}
	}
	return 0
}

func (s IngressServiceNodePort) Name() string { return s.Service.Name }

func (s IngressServiceNodePort) CreationTime() time.Time {
	return s.CreationTimestamp.Time
}

func (s IngressServiceNodePort) Addresses() []string {
	addrs := []string{}

	nodes, err := s.coreClient.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return nil // TODO propagate error
	}

	if len(nodes.Items) == 1 && nodes.Items[0].Name == "minikube" {
		// corev1.InternalIP type addresss may point to inaccessible IP, hence shell out...
		outBytes, err := exec.Command("minikube", "ip").Output()
		if err != nil {
			return nil // TODO propagate error
		}

		out := strings.TrimSpace(string(outBytes))

		if net.ParseIP(out) != nil {
			return []string{out}
		}

		return nil
	}

	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			switch addr.Type {
			case corev1.NodeHostName, corev1.NodeExternalIP, corev1.NodeExternalDNS:
				addrs = append(addrs, addr.Address)
			}
		}
	}

	return addrs
}

func (s IngressServiceNodePort) Ports() []int32 {
	ports := []int32{}

	for _, port := range s.Spec.Ports {
		ports = append(ports, port.NodePort)
	}

	return ports
}

func (s IngressServiceNodePort) MappedPort(port int32) int32 {
	for _, p := range s.Spec.Ports {
		if p.Port == port {
			return p.NodePort
		}
	}
	return 0
}
