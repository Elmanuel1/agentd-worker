# The controller serves GET /work-item over TLS at a private DNS name; sandboxes trust the CA made
# here. The keys live in Terraform state and Secrets Manager, so the state must be encrypted.
resource "aws_service_discovery_private_dns_namespace" "this" {
  name = "${var.name}.internal"
  vpc  = var.vpc_id
}

resource "aws_service_discovery_service" "controller" {
  name = "controller"
  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.this.id
    dns_records {
      type = "A"
      ttl  = 10
    }
  }
}

locals {
  handoff_host = "controller.${aws_service_discovery_private_dns_namespace.this.name}"
}

resource "tls_private_key" "ca" {
  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "tls_self_signed_cert" "ca" {
  private_key_pem       = tls_private_key.ca.private_key_pem
  is_ca_certificate     = true
  validity_period_hours = 24 * 365 * 5
  allowed_uses          = ["cert_signing", "crl_signing"]
  subject {
    common_name = "${var.name} handoff CA"
  }
}

resource "tls_private_key" "handoff" {
  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "tls_cert_request" "handoff" {
  private_key_pem = tls_private_key.handoff.private_key_pem
  dns_names       = [local.handoff_host]
  subject {
    common_name = local.handoff_host
  }
}

resource "tls_locally_signed_cert" "handoff" {
  cert_request_pem      = tls_cert_request.handoff.cert_request_pem
  ca_private_key_pem    = tls_private_key.ca.private_key_pem
  ca_cert_pem           = tls_self_signed_cert.ca.cert_pem
  validity_period_hours = 24 * 365
  early_renewal_hours   = 24 * 30
  allowed_uses          = ["digital_signature", "server_auth"]
}

resource "aws_secretsmanager_secret" "handoff_key" {
  name = "${var.name}/handoff-key"
}

resource "aws_secretsmanager_secret_version" "handoff_key" {
  secret_id     = aws_secretsmanager_secret.handoff_key.id
  secret_string = tls_private_key.handoff.private_key_pem
}
