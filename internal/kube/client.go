package kube

import (
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func NewClientset(cfg *rest.Config, version string) (*kubernetes.Clientset, error) {
	// 调优在这里做，root.go 保持干净
	cfg.UserAgent = "ekvols/" + version
	cfg.QPS = 20
	cfg.Burst = 40
	cfg.Timeout = 30 * time.Second // 需要的话再加

	return kubernetes.NewForConfig(cfg)
}
