# "<policy_id>/<cloud_provider>/<cloud_provider_account_id>/<instance_id>"
terraform import lucidity_scaling_policy_attachment.orders_db 33/AWS/123456789012/i-0abc123def4567890

# Azure: "<policy_id>/AZURE/<subscription_id>/<resource_group>/<vm_name>"
terraform import lucidity_scaling_policy_attachment.azure_vm 33/AZURE/8f2d1c3a-5b6e-4f70-9a81-2c3d4e5f6a7b/prod-rg/vm-db-01
