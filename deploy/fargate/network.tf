data "aws_region" "current" {}

data "aws_vpc_endpoint" "s3" {
  vpc_id       = var.vpc_id
  service_name = "com.amazonaws.${data.aws_region.current.region}.s3"
}

resource "aws_security_group" "controller" {
  name        = "${var.name}-controller"
  description = "agentd-worker controller"
  vpc_id      = var.vpc_id
}

resource "aws_security_group" "sandbox" {
  name        = "${var.name}-sandbox"
  description = "agentd sandboxes: outbound only to the proxy, agentd, the handoff port and EFS"
  vpc_id      = var.vpc_id
}

resource "aws_security_group" "efs" {
  name        = "${var.name}-efs"
  description = "Session workspaces"
  vpc_id      = var.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "handoff" {
  security_group_id            = aws_security_group.controller.id
  referenced_security_group_id = aws_security_group.sandbox.id
  ip_protocol                  = "tcp"
  from_port                    = 8443
  to_port                      = 8443
}

resource "aws_vpc_security_group_egress_rule" "controller" {
  security_group_id = aws_security_group.controller.id
  description       = "agentd, the AWS APIs and EFS through the VPC's endpoints"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
}

resource "aws_vpc_security_group_egress_rule" "controller_efs" {
  security_group_id            = aws_security_group.controller.id
  referenced_security_group_id = aws_security_group.efs.id
  ip_protocol                  = "tcp"
  from_port                    = 2049
  to_port                      = 2049
}

resource "aws_vpc_security_group_egress_rule" "sandbox_proxy" {
  security_group_id            = aws_security_group.sandbox.id
  referenced_security_group_id = var.proxy_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = var.proxy_port
  to_port                      = var.proxy_port
}

resource "aws_vpc_security_group_egress_rule" "sandbox_agentd" {
  security_group_id = aws_security_group.sandbox.id
  cidr_ipv4         = var.agentd_cidr
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
}

resource "aws_vpc_security_group_egress_rule" "sandbox_handoff" {
  security_group_id            = aws_security_group.sandbox.id
  referenced_security_group_id = aws_security_group.controller.id
  ip_protocol                  = "tcp"
  from_port                    = 8443
  to_port                      = 8443
}

resource "aws_vpc_security_group_egress_rule" "sandbox_efs" {
  security_group_id            = aws_security_group.sandbox.id
  referenced_security_group_id = aws_security_group.efs.id
  ip_protocol                  = "tcp"
  from_port                    = 2049
  to_port                      = 2049
}

# Fargate pulls the task's images over its own interface: ECR's layers come from S3.
resource "aws_vpc_security_group_egress_rule" "sandbox_image_layers" {
  security_group_id = aws_security_group.sandbox.id
  prefix_list_id    = data.aws_vpc_endpoint.s3.prefix_list_id
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
}

resource "aws_vpc_security_group_ingress_rule" "efs" {
  for_each                     = { controller = aws_security_group.controller.id, sandbox = aws_security_group.sandbox.id }
  security_group_id            = aws_security_group.efs.id
  referenced_security_group_id = each.value
  ip_protocol                  = "tcp"
  from_port                    = 2049
  to_port                      = 2049
}

resource "aws_vpc_security_group_egress_rule" "sandbox_endpoints" {
  security_group_id            = aws_security_group.sandbox.id
  description                  = "ECR and CloudWatch Logs, for the task's image pulls and logs"
  referenced_security_group_id = var.endpoints_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 443
  to_port                      = 443
}
