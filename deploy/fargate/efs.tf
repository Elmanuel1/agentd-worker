# Each session's /workspace is /sessions/<id> on this file system, reached through an access point
# the controller creates per session; the controller mounts the root to delete a released session's
# directory.
resource "aws_efs_file_system" "workspaces" {
  encrypted        = true
  performance_mode = "generalPurpose"
  throughput_mode  = "elastic"
  tags             = { Name = "${var.name}-workspaces" }
}

resource "aws_efs_mount_target" "workspaces" {
  for_each        = toset(var.private_subnet_ids)
  file_system_id  = aws_efs_file_system.workspaces.id
  subnet_id       = each.value
  security_groups = [aws_security_group.efs.id]
}

resource "aws_efs_file_system_policy" "workspaces" {
  file_system_id = aws_efs_file_system.workspaces.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "TLSOnly"
      Effect    = "Deny"
      Principal = { AWS = "*" }
      Action    = "*"
      Resource  = aws_efs_file_system.workspaces.arn
      Condition = { Bool = { "aws:SecureTransport" = "false" } }
    }]
  })
}
