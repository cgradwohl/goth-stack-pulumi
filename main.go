package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ecr"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ecs"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lb"
	ec2x "github.com/pulumi/pulumi-awsx/sdk/v2/go/awsx/ec2"
	"github.com/pulumi/pulumi-docker/sdk/v4/go/docker"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// create a vpc for the ALB
		strategy := ec2x.SubnetAllocationStrategy("Auto")
		vpcCidrBlock := "10.0.0.0/16"

		vpc, err := ec2x.NewVpc(ctx, "vpc", &ec2x.VpcArgs{
			EnableDnsHostnames: pulumi.Bool(true),
			CidrBlock:          &vpcCidrBlock,
			SubnetStrategy:     &strategy,
		})

		if err != nil {
			return err
		}

		// create a target group for the ALB
		targetGroup, err := lb.NewTargetGroup(ctx, "my-target-group", &lb.TargetGroupArgs{
			Port:     pulumi.Int(80),
			Protocol: pulumi.String("HTTP"),
			// If your service's task definition uses the awsvpc network mode (which is required for the Fargate launch type), you must choose IP addresses as the target type This is because tasks that use the awsvpc network mode are associated with an elastic network interface, not an Amazon EC2 instance.
			TargetType:    pulumi.String("ip"),
			IpAddressType: pulumi.String("ipv4"),
			VpcId:         vpc.VpcId,
		})
		if err != nil {
			return err
		}

		// create a SecurityGroup for the ALB
		// permits HTTP ingress and unrestricted egress.
		securityGroup, err := ec2.NewSecurityGroup(ctx, "allowTls", &ec2.SecurityGroupArgs{
			Description: pulumi.String("Allow TLS inbound traffic"),
			VpcId:       vpc.VpcId,
			Ingress: ec2.SecurityGroupIngressArray{
				&ec2.SecurityGroupIngressArgs{
					FromPort: pulumi.Int(80),
					ToPort:   pulumi.Int(80),
					Protocol: pulumi.String("tcp"),
					// This option automatically adds the 0.0.0.0/0 IPv4 CIDR block as the source. This is acceptable for a short time in a test environment, but it's unsafe in production environments. In production, authorize only a specific IP address or range of addresses to access your instance.
					CidrBlocks: pulumi.StringArray{
						pulumi.String("0.0.0.0/0"),
					},
				},
			},
			Egress: ec2.SecurityGroupEgressArray{
				&ec2.SecurityGroupEgressArgs{
					FromPort: pulumi.Int(0),
					ToPort:   pulumi.Int(0),
					Protocol: pulumi.String("-1"),
					CidrBlocks: pulumi.StringArray{
						pulumi.String("0.0.0.0/0"),
					},
				},
			},
		})
		if err != nil {
			return err
		}

		// create ALB
		alb, err := lb.NewLoadBalancer(ctx, "my-alb", &lb.LoadBalancerArgs{
			Internal:         pulumi.Bool(false),
			IpAddressType:    pulumi.String("ipv4"),
			LoadBalancerType: pulumi.String("application"),
			Name:             pulumi.String("my-alb"),
			Subnets:          vpc.PublicSubnetIds,
			SecurityGroups: pulumi.StringArray{
				securityGroup.ID().ToStringOutput(),
			},
		})
		if err != nil {
			return err
		}

		// create ALB Listener
		_, err = lb.NewListener(ctx, "my-alb-listener", &lb.ListenerArgs{
			LoadBalancerArn: alb.Arn,
			Port:            pulumi.Int(80),
			Protocol:        pulumi.String("HTTP"),
			DefaultActions: lb.ListenerDefaultActionArray{
				&lb.ListenerDefaultActionArgs{
					Type:           pulumi.String("forward"),
					TargetGroupArn: targetGroup.Arn,
				},
			},
		})
		if err != nil {
			return err
		}

		// Create ECR repository
		ecrRepository, err := ecr.NewRepository(ctx, "goth-stack-repo", &ecr.RepositoryArgs{
			Name: pulumi.String("goth-docker-repository"),
			ImageScanningConfiguration: &ecr.RepositoryImageScanningConfigurationArgs{
				ScanOnPush: pulumi.Bool(true),
			},
		})
		if err != nil {
			return err
		}

		// Create an auth token for the ECR repository
		authToken := ecr.GetAuthorizationTokenOutput(ctx, ecr.GetAuthorizationTokenOutputArgs{
			RegistryId: ecrRepository.RegistryId,
		}, nil)

		// Build the Docker image from local files
		// Push the Docker image to ECR
		image, err := docker.NewImage(ctx, "my-app-image", &docker.ImageArgs{
			Build: &docker.DockerBuildArgs{
				Args: pulumi.StringMap{
					"BUILDKIT_INLINE_CACHE": pulumi.String("1"),
				},
				CacheFrom: &docker.CacheFromArgs{
					Images: pulumi.StringArray{
						ecrRepository.RepositoryUrl.ApplyT(func(repositoryUrl string) (string, error) {
							return fmt.Sprintf("%v:latest", repositoryUrl), nil
						}).(pulumi.StringOutput),
					},
				},
				Context:    pulumi.String("./"),
				Dockerfile: pulumi.String("Dockerfile"),
				Platform:   pulumi.String("linux/arm64"),
			},
			ImageName: ecrRepository.RepositoryUrl.ApplyT(func(repositoryUrl string) (string, error) {
				return fmt.Sprintf("%v:latest", repositoryUrl), nil
			}).(pulumi.StringOutput),
			Registry: &docker.RegistryArgs{
				Username: pulumi.String("AWS"),
				Password: authToken.ApplyT(func(authToken ecr.GetAuthorizationTokenResult) (*string, error) {
					return &authToken.Password, nil
				}).(pulumi.StringPtrOutput).ToStringPtrOutput(),
				Server: ecrRepository.RepositoryUrl,
			},
		})
		if err != nil {
			return err
		}

		// exampleKey, err := kms.NewKey(ctx, "exampleKey", &kms.KeyArgs{
		// 	Description:          pulumi.String("example"),
		// 	DeletionWindowInDays: pulumi.Int(7),
		// })
		// if err != nil {
		// 	return err
		// }
		logGroup, err := cloudwatch.NewLogGroup(ctx, "my-log-group", nil)
		if err != nil {
			return err
		}
		ecsCluster, err := ecs.NewCluster(ctx, "test", &ecs.ClusterArgs{
			Configuration: &ecs.ClusterConfigurationArgs{
				ExecuteCommandConfiguration: &ecs.ClusterConfigurationExecuteCommandConfigurationArgs{
					// NOTE:Specify an AWS Key Management Service key ID to encrypt the data between the local client and the container. when should you do this?
					// KmsKeyId: exampleKey.Arn,
					Logging: pulumi.String("OVERRIDE"),
					LogConfiguration: &ecs.ClusterConfigurationExecuteCommandConfigurationLogConfigurationArgs{
						CloudWatchEncryptionEnabled: pulumi.Bool(true),
						CloudWatchLogGroupName:      logGroup.Name,
					},
				},
			},
		})
		if err != nil {
			return err
		}

		containerDefinition := image.ImageName.ApplyT(func(name string) (string, error) {
			fmtstr := `[{
				"name": "goth-app",
				"image": %q,
				"portMappings": [{
					"containerPort": 80,
					"hostPort": 80,
					"protocol": "tcp"
				}],
				"logConfiguration": {
					"logDriver": "awslogs",
					"options": {
						"awslogs-create-group": "true",
						"awslogs-group": "my-log-group",
						"awslogs-region": "us-east-1",
						"awslogs-stream-prefix": "goth-stack-pulumi"
					}
				}
			}]`
			return fmt.Sprintf(fmtstr, name), nil
		}).(pulumi.StringOutput)

		// create the ECS task execution IAM role (trust policy)
		// the task execution role grants the Amazon ECS container and Fargate agents permission to make AWS API calls on your behalf.
		//  this is required for things like pulling from ECR, logging to CloudWatch, etc.
		taskExecutionRole, err := iam.NewRole(ctx, "my-task-exec-role", &iam.RoleArgs{
			AssumeRolePolicy: pulumi.String(`{
					"Version": "2012-10-17",
					"Statement": [
							{
									"Effect": "Allow",
									"Principal": {
											"Service": "ecs-tasks.amazonaws.com"
									},
									"Action": "sts:AssumeRole"
							}
					]
			}`),
		})

		// attach the AWS managed policy (AmazonECSTaskExecutionRolePolicy) for Fargate to the task execution role
		_, err = iam.NewRolePolicyAttachment(ctx, "my-role-attachment", &iam.RolePolicyAttachmentArgs{
			Role:      taskExecutionRole.Name,
			PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"),
		})

		ecsTask, err := ecs.NewTaskDefinition(ctx, "my-ecs-task", &ecs.TaskDefinitionArgs{
			ContainerDefinitions: containerDefinition,
			Cpu:                  pulumi.String("256"),
			ExecutionRoleArn:     taskExecutionRole.Arn,
			Family:               pulumi.String("goth-task"),
			Memory:               pulumi.String("512"),
			NetworkMode:          pulumi.String("awsvpc"),
			RequiresCompatibilities: pulumi.StringArray{
				pulumi.String("FARGATE"),
			},
			// I do no think we need this for now since this is the ARN of IAM role that allows your Amazon ECS container task to make calls to other AWS services.
			// we probaly need this is we want to use AWS CloudWatch Logs logging driver?
			// TaskRoleArn: ,
		})

		_, err = ecs.NewService(ctx, "my-ecs-service", &ecs.ServiceArgs{
			Cluster:        ecsCluster.Arn,
			DesiredCount:   pulumi.Int(1),
			LaunchType:     pulumi.String("FARGATE"),
			TaskDefinition: ecsTask.TaskRoleArn,
			NetworkConfiguration: &ecs.ServiceNetworkConfigurationArgs{
				AssignPublicIp: pulumi.Bool(true),
				SecurityGroups: pulumi.StringArray{
					securityGroup.ID().ToStringOutput(),
				},
				Subnets: vpc.PublicSubnetIds,
			},
		})
		if err != nil {
			return err
		}

		// Pulumi Exports
		ctx.Export("url", alb.DnsName)
		return nil
	})
}
