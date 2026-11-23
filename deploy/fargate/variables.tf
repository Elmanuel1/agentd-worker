variable "region" {
  description = "The AWS region to deploy in."
  type        = string
}

variable "name" {
  description = "Prefix of every resource."
  type        = string
  default     = "agentd-worker"
}

variable "vpc_id" {
  description = "The VPC the controller, the sandboxes and EFS run in."
  type        = string
}

variable "private_subnet_ids" {
  description = "Subnets for the controller and the sandboxes, with no route to the internet."
  type        = list(string)
}

variable "agentd_base_url" {
  description = "agentd's API as the controller and sandboxes reach it."
  type        = string
}

variable "agentd_cidr" {
  description = "The addresses agentd answers on, the only API sandboxes may reach directly."
  type        = string
}

variable "proxy_security_group_id" {
  description = "The egress proxy's security group; sandboxes reach it on proxy_port."
  type        = string
}

variable "proxy_port" {
  description = "The egress proxy's port."
  type        = number
  default     = 3128
}

variable "environment_id" {
  description = "The environment the controller serves, or runtime for agentd's runtime queue."
  type        = string
}

variable "environment_key_secret_arn" {
  description = "The Secrets Manager secret holding the environment key, or agentd's runtime key."
  type        = string
}

variable "controller_image" {
  description = "agentd-worker's image in ECR, by digest."
  type        = string
  validation {
    condition     = can(regex("^[0-9]{12}\\.dkr\\.ecr\\.[a-z0-9-]+\\.amazonaws\\.com/[^@:]+(:[^@]+)?@sha256:[0-9a-f]{64}$", var.controller_image))
    error_message = "An image in ECR by digest: sandboxes reach no other registry."
  }
}

variable "sandbox_image" {
  description = "agentd's sandbox image in ECR, by digest."
  type        = string
  validation {
    condition     = can(regex("^[0-9]{12}\\.dkr\\.ecr\\.[a-z0-9-]+\\.amazonaws\\.com/[^@:]+(:[^@]+)?@sha256:[0-9a-f]{64}$", var.sandbox_image))
    error_message = "An image in ECR by digest: sandboxes reach no other registry."
  }
}

variable "max_sandboxes" {
  description = "Items the controller handles at once."
  type        = number
  default     = 100
}

variable "sandbox_cpu" {
  description = "A sandbox task's CPU units."
  type        = string
  default     = "512"
}

variable "sandbox_memory" {
  description = "A sandbox task's memory in MiB."
  type        = string
  default     = "1024"
}

variable "otel_exporter_otlp_endpoint" {
  description = "Where the controller sends its metrics; empty sends none."
  type        = string
  default     = ""
}

variable "endpoints_security_group_id" {
  description = "The security group of the VPC's interface endpoints; it must allow TCP 443 from both of this module's security groups."
  type        = string
}

variable "retention_days" {
  description = "Deletes the storage of a session idle this many days; 0 keeps it until the session is deleted or archived."
  type        = number
  default     = 0
}
