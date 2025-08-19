package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ebs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

type Clients struct {
	EBS *ebs.Client
	EKS *eks.Client
	EC2 *ec2.Client
}

// New 创建 AWS 客户端，支持 --region / AWS_REGION 环境变量 / IRSA / 本地 ~/.aws/config
func New(ctx context.Context) (*Clients, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithClientLogMode(aws.LogRetries), // 可选：打印重试日志
	}

	// 自动解析: env -> shared config (~/.aws/credentials) -> EC2 IMDS/IRSA
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &Clients{
		EBS: ebs.NewFromConfig(cfg),
		EKS: eks.NewFromConfig(cfg),
		EC2: ec2.NewFromConfig(cfg),
	}, nil
}
