package exporter

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/apigateway"
	"github.com/aws/aws-sdk-go/service/apigateway/apigatewayiface"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	r "github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go/service/sts"
	log "github.com/sirupsen/logrus"
)

// from aws_cloudwatch.go

func createStsSession(roleArn string, debug bool) *sts.STS {
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	maxStsRetries := 5
	config := &aws.Config{MaxRetries: &maxStsRetries}
	if debug {
		config.LogLevel = aws.LogLevel(aws.LogDebugWithHTTPBody)
	}
	if roleArn != "" {
		config.Credentials = stscreds.NewCredentials(sess, roleArn)
	}
	return sts.New(sess, config)
}

func createCloudwatchSession(region *string, roleArn string, fips, debug bool) *cloudwatch.CloudWatch {
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
		Config:            aws.Config{Region: aws.String(*region)},
	}))

	maxCloudwatchRetries := 5

	config := &aws.Config{Region: region, MaxRetries: &maxCloudwatchRetries}

	if fips {
		// https://docs.aws.amazon.com/general/latest/gr/cw_region.html
		endpoint := fmt.Sprintf("https://monitoring-fips.%s.amazonaws.com", *region)
		config.Endpoint = aws.String(endpoint)
	}

	if debug {
		config.LogLevel = aws.LogLevel(aws.LogDebugWithHTTPBody)
	}

	if roleArn != "" {
		config.Credentials = stscreds.NewCredentials(sess, roleArn)
	}

	return cloudwatch.New(sess, config)
}

// from aws_tags.go

func createSession(roleArn string, config *aws.Config) *session.Session {
	sess, err := session.NewSession(config)
	if err != nil {
		log.Fatalf("Failed to create session due to %v", err)
	}
	if roleArn != "" {
		config.Credentials = stscreds.NewCredentials(sess, roleArn)
	}
	return sess
}

func createTagSession(region *string, roleArn string, fips bool) *r.ResourceGroupsTaggingAPI {
	maxResourceGroupTaggingRetries := 5
	config := &aws.Config{Region: region, MaxRetries: &maxResourceGroupTaggingRetries}
	if fips {
		// ToDo: Resource Groups Tagging API does not have FIPS compliant endpoints
		// https://docs.aws.amazon.com/general/latest/gr/arg.html
		// endpoint := fmt.Sprintf("https://tagging-fips.%s.amazonaws.com", *region)
		// config.Endpoint = aws.String(endpoint)
	}
	return r.New(createSession(roleArn, config), config)
}

func createASGSession(region *string, roleArn string, fips bool) autoscalingiface.AutoScalingAPI {
	maxAutoScalingAPIRetries := 5
	config := &aws.Config{Region: region, MaxRetries: &maxAutoScalingAPIRetries}
	if fips {
		// ToDo: Autoscaling does not have a FIPS endpoint
		// https://docs.aws.amazon.com/general/latest/gr/autoscaling_region.html
		// endpoint := fmt.Sprintf("https://autoscaling-plans-fips.%s.amazonaws.com", *region)
		// config.Endpoint = aws.String(endpoint)
	}
	return autoscaling.New(createSession(roleArn, config), config)
}

func createEC2Session(region *string, roleArn string, fips bool) ec2iface.EC2API {
	maxEC2APIRetries := 10
	config := &aws.Config{Region: region, MaxRetries: &maxEC2APIRetries}
	if fips {
		// https://docs.aws.amazon.com/general/latest/gr/ec2-service.html
		endpoint := fmt.Sprintf("https://ec2-fips.%s.amazonaws.com", *region)
		config.Endpoint = aws.String(endpoint)
	}
	return ec2.New(createSession(roleArn, config), config)
}

func createAPIGatewaySession(region *string, roleArn string, fips bool) apigatewayiface.APIGatewayAPI {
	maxApiGatewaygAPIRetries := 5
	config := &aws.Config{Region: region, MaxRetries: &maxApiGatewaygAPIRetries}
	sess, err := session.NewSession(config)
	if err != nil {
		log.Fatal(err)
	}
	if roleArn != "" {
		config.Credentials = stscreds.NewCredentials(sess, roleArn)
	}
	if fips {
		// https://docs.aws.amazon.com/general/latest/gr/apigateway.html
		endpoint := fmt.Sprintf("https://apigateway-fips.%s.amazonaws.com", *region)
		config.Endpoint = aws.String(endpoint)
	}
	return apigateway.New(sess, config)
}
