// Package k8s provides utilities for communicating with Kubernetes cluster.
/*
 * Copyright (c) 2018-2021, NVIDIA CORPORATION. All rights reserved.
 */
package k8s

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/NVIDIA/aistore/3rdparty/glog"
	v1 "k8s.io/api/core/v1"
)

const (
	k8sPodNameEnv  = "HOSTNAME"
	k8sNodeNameEnv = "K8S_NODE_NAME"

	Default = "default"
	Pod     = "pod"
	Svc     = "svc"
)

var (
	detectOnce sync.Once
	NodeName   string
)

func initDetect() {
	var (
		pod *v1.Pod

		nodeName = os.Getenv(k8sNodeNameEnv)
		podName  = os.Getenv(k8sPodNameEnv)
	)

	glog.Infof(
		"Verifying type of deployment (%s: %q, %s: %q)",
		k8sPodNameEnv, podName, k8sNodeNameEnv, nodeName,
	)

	client, err := GetClient()
	if err != nil {
		glog.Infof("Couldn't initiate a K8s client, assuming non-Kubernetes deployment")
		return
	}

	// If the `k8sNodeNameEnv` is set then we should just use it as we trust it
	// more than anything else.
	if nodeName != "" {
		goto checkNode
	}

	if podName == "" {
		glog.Infof("%s environment not found, assuming non-Kubernetes deployment", k8sPodNameEnv)
		return
	}

	pod, err = client.Pod(podName)
	if err != nil {
		glog.Errorf("Failed to get pod %q, err: %v. Try setting %q env variable", podName, err, k8sNodeNameEnv)
		return
	}
	nodeName = pod.Spec.NodeName

checkNode:
	node, err := client.Node(nodeName)
	if err != nil {
		glog.Errorf("Failed to get node %q, err: %v. Try setting %q env variable", nodeName, err, k8sNodeNameEnv)
		return
	}

	NodeName = node.Name
	glog.Infof("Successfully got node name %q, assuming Kubernetes deployment", NodeName)
}

func Detect() error {
	detectOnce.Do(initDetect)

	if NodeName == "" {
		return fmt.Errorf("operation requires Kubernetes deployment")
	}
	return nil
}

func CleanName(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), "_", "-")
}
