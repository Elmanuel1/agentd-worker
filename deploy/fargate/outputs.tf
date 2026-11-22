output "cluster" {
  value = aws_ecs_cluster.this.name
}

output "sandbox_security_group_id" {
  description = "Allow it in the proxy's and agentd's ingress."
  value       = aws_security_group.sandbox.id
}

output "controller_security_group_id" {
  description = "Allow it in agentd's ingress."
  value       = aws_security_group.controller.id
}

output "workspaces_file_system_id" {
  value = aws_efs_file_system.workspaces.id
}
