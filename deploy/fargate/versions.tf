# agentd-worker on ECS Fargate for one environment: the controller as a service, sandboxes as tasks
# it runs, each session's /workspace on EFS. docs/fargate.md has the steps.
terraform {
  required_version = ">= 1.9"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

provider "aws" {
  region = var.region
}
