# Configure Mattermost Agents with AWS Bedrock

This guide walks you through setting up the Mattermost Agents plugin with [Amazon Bedrock](https://aws.amazon.com/bedrock/) as the LLM provider. When running Mattermost on AWS EC2 infrastructure, you can use IAM instance profiles for seamless, credential-free authentication.

## Prerequisites

Before you begin, ensure you have:

- A Mattermost server (v10.11 or later) running on an AWS EC2 instance
- An active Mattermost Enterprise license
- AWS CLI configured with permissions to create IAM resources
- Access to the Mattermost System Console as a system administrator

## Enabling model access

Before creating IAM resources, ensure the models you want to use are enabled in your AWS account.

### Anthropic Claude models

Anthropic requires a one-time use case submission (First Time Use or FTU form) before you can invoke their models via Bedrock. This is required once per AWS account, or once on the organization management account if using AWS Organizations.

**Via AWS Console:**

1. Open the [AWS Bedrock Model Catalog](https://console.aws.amazon.com/bedrock/home#/model-catalog)
2. Look for the banner prompting you to **Submit use case details**
3. Fill in your company name, website, intended users, industry, and use case, then submit

**Via AWS CLI:**

```bash
FORM_DATA=$(printf '%s' '{
    "companyName": "Your Company",
    "companyWebsite": "https://yourcompany.com",
    "intendedUsers": "internal employees",
    "industryOption": "Technology",
    "useCases": "AI-powered chat assistance for internal teams"
}' | base64)
aws bedrock put-use-case-for-model-access --form-data "$FORM_DATA" --region us-east-1
```

#### Inference profiles for newer Claude models

Newer Anthropic models (such as Claude Sonnet latest) cannot be invoked using their base model ID — you must use a Cross-Region Inference Profile ID instead. If you attempt to use the raw model ID, you will see:

```text
ValidationException: Invocation of model ID anthropic.claude-sonnet-4-6 with on-demand
throughput isn't supported. Retry your request with the ID or ARN of an inference profile
that contains this model.
```

Use the **inference** profile prefix in the **Default Model** field in Mattermost:

| Model | Inference Profile ID |
|-------|---------------------|
| Claude Sonnet 4.6 (US routing) | `us.anthropic.claude-sonnet-4-6` |
| Claude Sonnet 4.6 (global routing) | `global.anthropic.claude-sonnet-4-6` |

### Other Bedrock models

For Amazon (Nova, Titan), Meta (Llama), Mistral, and other non-Anthropic models:

1. Go to the Bedrock console → **Model access**
2. Select the models you want to enable
3. Select **Request model access**

Most non-Anthropic models do not require an FTU form.

## Step 1: Create IAM resources for Bedrock access

Create an IAM policy, role, and instance profile that grants your Mattermost EC2 instances permission to call Amazon Bedrock.

### Create the IAM policy

Create a policy that allows the required Bedrock API actions:

```bash
aws iam create-policy \
  --policy-name MattermostBedrockAccess \
  --policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Action": [
        "bedrock:InvokeModel",
        "bedrock:InvokeModelWithResponseStream"
      ],
      "Resource": [
        "arn:aws:bedrock:*::foundation-model/*",
        "arn:aws:bedrock:*:<YOUR_ACCOUNT_ID>:inference-profile/*"
      ]
    }]
  }'
```

Replace `<YOUR_ACCOUNT_ID>` with your 12-digit AWS account ID.

> **Note:** Although the Mattermost Agents plugin uses the Bedrock Converse API internally, the correct IAM action names are `bedrock:InvokeModel` and `bedrock:InvokeModelWithResponseStream` — `bedrock:Converse` and `bedrock:ConverseStream` are not valid IAM policy actions. The `inference-profile/*` resource ARN is required for Anthropic Claude models, which use Cross-Region Inference Profiles.
> **Tip:** To restrict access to specific models, replace the wildcard ARNs with model-specific ARNs. For Anthropic Claude models, you need entries in both the `foundation-model` and `inference-profile` resources. For example: `arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-sonnet-4-6` and `arn:aws:bedrock:us-east-1:<YOUR_ACCOUNT_ID>:inference-profile/us.anthropic.claude-sonnet-4-6`.

### Create the IAM role and instance profile

Create an IAM role that EC2 instances can assume, then attach the Bedrock policy:

```bash
# Create the role with an EC2 trust policy
aws iam create-role \
  --role-name MattermostBedrockRole \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principal": {"Service": "ec2.amazonaws.com"},
      "Action": "sts:AssumeRole"
    }]
  }'

# Attach the Bedrock policy
aws iam attach-role-policy \
  --role-name MattermostBedrockRole \
  --policy-arn arn:aws:iam::<YOUR_ACCOUNT_ID>:policy/MattermostBedrockAccess

# Create an instance profile and add the role
aws iam create-instance-profile \
  --instance-profile-name MattermostBedrockProfile

aws iam add-role-to-instance-profile \
  --instance-profile-name MattermostBedrockProfile \
  --role-name MattermostBedrockRole
```

Replace `<YOUR_ACCOUNT_ID>` with your AWS account ID.

### Attach the instance profile to your EC2 instances

If your Mattermost server is already running, attach the instance profile to the running instance:

```bash
aws ec2 associate-iam-instance-profile \
  --iam-instance-profile Name=MattermostBedrockProfile \
  --instance-id <YOUR_INSTANCE_ID>
```

For new deployments, specify the instance profile when launching the EC2 instance or configure it in your infrastructure-as-code (Terraform, CloudFormation, etc.).

> **Note:** If you have multiple Mattermost app server instances, attach the instance profile to all of them.

## Step 2: Install the Agents plugin

The Mattermost Agents plugin may be pre-packaged with your Mattermost installation. Check your current version and upgrade if needed.

1. Go to **System Console > Plugins > Plugin Management**.
2. If the **Agents** plugin is listed, check its version. Version **1.9.0 or later** is required for native AWS Bedrock support with IAM instance profiles.
3. If the plugin is not installed or needs upgrading:
   a. Download the latest release from the [Mattermost Agents plugin releases page](https://github.com/mattermost/mattermost-plugin-agents/releases).
   b. Upload the `.tar.gz` file under **Upload Plugin** in Plugin Management.
   c. Once uploaded, select **Enable** to activate the plugin.

Alternatively, install via `mmctl` on the server:

```bash
/opt/mattermost/bin/mmctl plugin install-url \
  https://github.com/mattermost/mattermost-plugin-agents/releases/download/v1.9.0/mattermost-plugin-agents-v1.9.0-linux-amd64.tar.gz

/opt/mattermost/bin/mmctl plugin enable mattermost-ai
```

## Step 3: Configure the Bedrock service

1. Go to **System Console > Plugins > Agents**.
2. Under **Services**, select **Add a Service**.
3. Configure the following fields:

| Field | Value |
|-------|-------|
| **Service Name** | A descriptive name (e.g., `AWS Bedrock`) |
| **Service Type** | Select `AWS Bedrock` |
| **AWS Region** | The AWS region where Bedrock is available (e.g., `us-east-1`) |
| **Default Model** | The Bedrock model identifier (e.g., `us.anthropic.claude-sonnet-4-6`) |
| **AWS Access Key ID** | Leave empty when using IAM instance profiles |
| **AWS Secret Access Key** | Leave empty when using IAM instance profiles |
| **API Key** | Optional. Bedrock console API key (base64 encoded). Suitable for short-term testing only—keys expire after 12 hours. Leave empty when using IAM instance profiles or IAM user credentials, as those methods take precedence. |

4. Select **Save**.

> **Tip:** When running on EC2 with an IAM instance profile, leave the credential fields empty. The AWS SDK automatically discovers credentials from the instance metadata service. This is the recommended approach for production deployments on AWS.

### Available Bedrock models

Amazon Bedrock provides access to models from multiple providers. Common model identifiers include:

| Model | Identifier |
|-------|------------|
| Anthropic Claude Sonnet 4.6 | `us.anthropic.claude-sonnet-4-6` |
| Amazon Nova Pro | `amazon.nova-pro-v1:0` |
| Amazon Nova Lite | `amazon.nova-lite-v1:0` |
| Amazon Nova Micro | `amazon.nova-micro-v1:0` |

> **Note:** For Anthropic Claude models, use the inference profile ID (with `us.` or `global.` prefix) rather than the raw model ID. See [Enabling model access](#enabling-model-access) above.

Check [Amazon Bedrock model availability](https://docs.aws.amazon.com/bedrock/latest/userguide/models-supported.html) for the full list of supported models and regional availability.

## Step 4: Create an AI agent

1. Go to **System Console > Plugins > Agents**.
2. Under **Agents**, select **Add an Agent**.
3. Configure the following fields:

| Field | Value |
|-------|-------|
| **Agent Name** | The username for the bot (e.g., `ai`) |
| **Display Name** | The display name shown in conversations (e.g., `AI Assistant`) |
| **Service** | Select the Bedrock service you created in Step 3 |
| **Custom Instructions** | Optional system prompt for the agent's behavior |
| **Enable Vision** | Enable to allow the agent to process images |

4. Select **Save**.

## Step 5: Verify the setup

1. **Verify IAM credentials**: SSH into your Mattermost EC2 instance and run:

   ```bash
   curl http://169.254.169.254/latest/meta-data/iam/security-credentials/
   ```

   This should return `MattermostBedrockRole` (or your role name).

2. **Test the agent**: In Mattermost, start a direct message with the AI agent bot (e.g., `@ai`) and send a test message. The agent should respond using the configured Bedrock model.

3. **Check logs**: If the agent doesn't respond, review the Mattermost server logs for errors:

   ```bash
   tail -f /opt/mattermost/logs/mattermost.log | grep -i "bedrock\|mattermost-ai"
   ```

## Troubleshooting

| Issue | Resolution |
|-------|------------|
| `AccessDeniedException: not authorized to perform bedrock:InvokeModelWithResponseStream` | Ensure the IAM policy includes `bedrock:InvokeModel` and `bedrock:InvokeModelWithResponseStream`. Note that `bedrock:Converse` is not a valid IAM action. Also ensure the `inference-profile/*` ARN is included if using Anthropic Claude models. |
| `ValidationException: Invocation of model ID ... with on-demand throughput isn't supported` | You are using a raw model ID for a model that requires a Cross-Region Inference Profile. Use the inference profile ID instead (e.g., `us.anthropic.claude-sonnet-4-6` instead of `anthropic.claude-sonnet-4-6`). |
| `ThrottlingException: Too many tokens per day` | Your account may have a zero on-demand quota for this model. Check [Service Quotas](https://docs.aws.amazon.com/bedrock/latest/userguide/quotas.html) in the AWS console for your target region. New accounts may need to open an AWS Support ticket or try a different region. |
| `failed to load AWS config` | Verify the IAM instance profile is attached to the EC2 instance. Check with `curl http://169.254.169.254/latest/meta-data/iam/security-credentials/`. |
| Agent responds with "An error occurred while accessing the LLM" | Check the server logs for the specific error. Common causes: incorrect model ID, model not available in the selected region, or insufficient IAM permissions. |
| Plugin won't enable | Ensure you have a valid Mattermost Enterprise license and the plugin version is compatible with your server version. |

## Authentication options

While IAM instance profiles are recommended for EC2 deployments, the Agents plugin supports three authentication methods for Bedrock:

1. **IAM instance profile** (recommended for EC2): No credentials needed. The AWS SDK uses the EC2 metadata service automatically.
2. **IAM user credentials**: Enter the `AWS Access Key ID` and `AWS Secret Access Key` in the service configuration. Useful for non-EC2 deployments.
3. **Bedrock API key**: Enter a Bedrock console-generated API key in the `API Key` field. Suitable for short-term testing (keys expire after 12 hours).

Credentials are evaluated in the order listed above. If IAM credential fields are populated, they take precedence over the API key and the default credential chain.
