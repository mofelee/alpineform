variable "environment" {
  type        = string
  default     = "dev"
  nullable    = false
  description = "Deployment environment name."

  validation {
    condition     = length(var.environment) >= 2
    error_message = "environment must contain at least two characters"
  }
}

variable "listen_ports" {
  type    = list(number)
  default = [80, 443]
}

locals {
  deployment_label = "alpineform-${var.environment}"
}
