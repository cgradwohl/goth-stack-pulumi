package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ecr"
	"github.com/pulumi/pulumi-docker/sdk/v4/go/docker"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
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
		myAppImage, err := docker.NewImage(ctx, "my-app-image", &docker.ImageArgs{
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

		// Pulumi Exports
		ctx.Export("imageName", myAppImage.ImageName)
		return nil
	})
}
