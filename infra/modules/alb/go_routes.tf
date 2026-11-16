locals {
  go_native_routes = var.enable_go_native_routes ? {
    for route in jsondecode(file("${path.module}/../../runtime/go-stack.json")).native_routes : route.name => route
  } : {}
}

resource "aws_lb_target_group" "native_go" {
  for_each    = local.go_native_routes
  name        = "flowcard-go-${each.key}"
  port        = each.value.port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"
  health_check {
    path                = each.value.health
    matcher             = "200"
    interval            = 15
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

# Foundation creation does not switch existing callback traffic. The release
# controller removes the preview header only after the Go target is healthy.
resource "aws_lb_listener_rule" "native_go" {
  for_each     = local.go_native_routes
  listener_arn = aws_lb_listener.http.arn
  priority     = 100 + index(sort(keys(local.go_native_routes)), each.key)
  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.native_go[each.key].arn
  }
  condition {
    path_pattern { values = [each.value.path] }
  }
  condition {
    http_header {
      http_header_name = "x-flowcard-go-preview"
      values           = ["go-stack-v1"]
    }
  }
  lifecycle { ignore_changes = [condition] }
}

output "go_native_target_groups" {
  value = { for key, group in aws_lb_target_group.native_go : key => group.arn }
}

output "go_native_rule_arns" {
  value = { for key, rule in aws_lb_listener_rule.native_go : key => rule.arn }
}
