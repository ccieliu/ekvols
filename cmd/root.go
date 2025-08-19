/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"

	"ekvols/internal/aws"
	"ekvols/internal/kube"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

var (
	Version   string = "1.0Alpha"
	k8sFlags  *genericclioptions.ConfigFlags
	K8sClient *kubernetes.Clientset
	AwsClient *aws.Clients
)

func initK8sFlags() {
	// Initialize Kubernetes related flags here if needed
	k8sFlags = genericclioptions.NewConfigFlags(true)
	k8sFlags.AddFlags(rootCmd.PersistentFlags())
}

func initKubeClient() error {
	cfg, err := k8sFlags.ToRESTConfig()
	if err != nil {
		return err
	}

	client, err := kube.NewClientset(cfg, Version)
	if err != nil {
		return err
	}

	K8sClient = client
	return nil
}

func initAwsClient(ctx context.Context) error {
	cli, err := aws.New(ctx)
	if err != nil {
		return err
	}
	AwsClient = cli
	return nil
}

func initDepends(cmd *cobra.Command, args []string) error {
	if err := initKubeClient(); err != nil {
		return err
	}
	if err := initAwsClient(cmd.Context()); err != nil {
		return err
	}
	return nil
}

var rootCmd = &cobra.Command{
	Use:               "ekvols",
	Short:             "A brief description of your application",
	Long:              `A longer description...`,
	Run:               runRoot,
	Version:           Version,
	Args:              cobra.NoArgs,
	PersistentPreRunE: initDepends,
}

func runRoot(cmd *cobra.Command, args []string) {
	cmd.Help()
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		return err
	}
	return nil
}

func init() {
	initK8sFlags()
	rootCmd.SetVersionTemplate("Version: {{.Version}}\n")
}
