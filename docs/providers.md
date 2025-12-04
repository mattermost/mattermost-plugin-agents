# LLM Provider Configuration Guide

This guide covers configuring different Large Language Model (LLM) providers with the Mattermost Agents plugin. Each provider has specific configuration requirements and capabilities.

## Supported Providers

The Mattermost Agents plugin currently supports these LLM providers:

- Local models via OpenAI-compatible APIs (Ollama, vLLM, etc.)
- OpenAI
- Anthropic
- AWS Bedrock
- Cohere
- Mistral
- Azure OpenAI

## General Configuration Concepts

For any LLM provider, you'll need to configure API authentication (keys, tokens, or other authentication methods), model selection for different use cases, parameters like context length and token limits, and ensure proper connectivity to provider endpoints.

## Local Models (OpenAI Compatible)

The OpenAI Compatible option allows integration with any OpenAI-compatible LLM provider, such as [Ollama](https://ollama.com/):

### Configuration

1. Deploy your model, for example, on [Ollama](https://ollama.com/)
2. Select **OpenAI Compatible** in the **AI Service** dropdown
3. Enter the URL to your AI service from your Mattermost deployment in the **API URL** field. Be sure to include the port, and append `/v1` to the end of the URL if using Ollama. (e.g., `http://localhost:11434/v1` for Ollama, otherwise `http://localhost:11434/`)
4. If using Ollama, leave the **API Key** field blank
5. Specify your model name in the **Default Model** field

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API URL** | Yes | The endpoint URL for your OpenAI-compatible API |
| **API Key** | No | API key if your service requires authentication |
| **Default Model** | Yes | The model to use by default |
| **Organization ID** | No | Organization ID if your service supports it |
| **Send User ID** | No | Whether to send user IDs to the service |

### Special Considerations

Ensure your self-hosted solution has sufficient compute resources and test for compatibility with the Mattermost plugin. Some advanced features may not be available with all compatible providers, so adjust token limits based on your deployment's capabilities.

## OpenAI

### Authentication

Obtain an [OpenAI API key](https://platform.openai.com/account/api-keys), then select **OpenAI** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API. If your API key belongs to an OpenAI organization, you can optionally specify your **Organization ID**.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your OpenAI API key |
| **Organization ID** | No | Your OpenAI organization ID |
| **Default Model** | Yes | The model to use by default (see [OpenAI's model documentation](https://platform.openai.com/docs/models)) |
| **Send User ID** | No | Whether to send user IDs to OpenAI |

## Anthropic (Claude)

### Authentication

Obtain an [Anthropic API key](https://console.anthropic.com/settings/keys), then select **Anthropic** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Anthropic API key |
| **Default Model** | Yes | The model to use by default (see [Anthropic's model documentation](https://docs.anthropic.com/claude/docs/models-overview)) |

## AWS Bedrock

### Overview

AWS Bedrock provides access to multiple foundation models through a unified API, including models from Anthropic (Claude), Amazon (Titan), and other providers. Bedrock is ideal for organizations already using AWS infrastructure or those requiring sovereign AI deployments.

### Prerequisites

Before configuring Bedrock:

1. Ensure you have an active AWS account with access to Amazon Bedrock
2. Enable model access in the AWS Bedrock console for the models you want to use
3. Have appropriate IAM permissions for `bedrock:Converse` and `bedrock:ConverseStream`
4. Know which AWS region you'll be using (model availability varies by region)

### Authentication

AWS Bedrock supports multiple authentication methods:

#### Option 1: IAM Roles (Recommended for AWS deployments)

If your Mattermost server runs on AWS infrastructure (EC2, ECS, EKS), you can use IAM roles:

1. Create an IAM role with Bedrock permissions
2. Attach the role to your Mattermost infrastructure
3. In Mattermost, select **AWS Bedrock** in the **Service** dropdown
4. Enter your AWS region (e.g., `us-east-1`) in the **AWS Region** field
5. Leave the **API Key** field blank - AWS SDK will use the IAM role automatically

#### Option 2: Bedrock Console API Keys (Short-term)

For quick testing and development (valid for 12 hours):

1. Go to Amazon Bedrock console in your desired region
2. Click on your profile → **Generate API Key**
3. Copy the generated API key (format: `bedrock-api-key-...`)
4. In Mattermost, select **AWS Bedrock** in the **Service** dropdown
5. Enter your AWS region (e.g., `us-west-2`) in the **AWS Region** field
6. Paste the Bedrock API key directly in the **API Key** field

**Note**: Short-term API keys expire after 12 hours or when your console session ends. For production use, consider IAM user credentials or IAM roles.

#### Option 3: AWS IAM User Access Keys

For long-term production use with IAM user credentials, there are two ways to configure them:

**Method A: Using dedicated IAM credential fields (Recommended)**

1. Create an IAM user with programmatic access and Bedrock permissions (see IAM Policy Example below)
2. Generate AWS access keys for the IAM user
3. In Mattermost, select **AWS Bedrock** in the **Service** dropdown
4. Enter your AWS region (e.g., `us-west-2`) in the **AWS Region** field
5. Enter your AWS Access Key ID in the **AWS Access Key ID** field
6. Enter your AWS Secret Access Key in the **AWS Secret Access Key** field
7. Leave the **API Key** field blank

**Method B: Using the API Key field (Legacy format)**

1. Create an IAM user with programmatic access and Bedrock permissions
2. Generate AWS access keys for the IAM user
3. In Mattermost, select **AWS Bedrock** in the **Service** dropdown
4. Enter your AWS region (e.g., `us-west-2`) in the **AWS Region** field
5. Enter your IAM user credentials in the **API Key** field using the format: `access_key_id:secret_access_key`

**Note**: If both IAM credential fields and the API Key are provided, the IAM credential fields take precedence.

#### Option 4: Environment Variables

You can also configure AWS credentials through environment variables on your Mattermost server:

```bash
export AWS_ACCESS_KEY_ID=your_access_key
export AWS_SECRET_ACCESS_KEY=your_secret_key
export AWS_REGION=us-east-1
```

Then in Mattermost:
- Enter the region in the **AWS Region** field
- Leave the **AWS Access Key ID**, **AWS Secret Access Key**, and **API Key** fields blank

**Note**: Environment variables have the lowest precedence. Credentials configured in the System Console (IAM fields or API Key) will take precedence over environment variables.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **AWS Region** | Yes | AWS region where Bedrock is available (e.g., `us-east-1`, `us-west-2`, `eu-central-1`) |
| **Custom Endpoint URL** | No | Optional custom endpoint for VPC endpoints or proxies (e.g., `https://bedrock-runtime.vpce-xxx.us-east-1.vpce.amazonaws.com`). Leave blank for standard AWS endpoints. |
| **AWS Access Key ID** | No | IAM user access key ID for long-term credentials. Takes precedence over API Key if both are set. Can also be set via `AWS_ACCESS_KEY_ID` environment variable. |
| **AWS Secret Access Key** | No | IAM user secret access key. Required if AWS Access Key ID is provided. Can also be set via `AWS_SECRET_ACCESS_KEY` environment variable. |
| **API Key** | No | Bedrock console API key (base64 encoded, format: `ABSKQm...`). If IAM credentials above are set, they take precedence. Can also use environment variables or IAM roles. |
| **Default Model** | Yes | The Bedrock model ID to use (e.g., `anthropic.claude-3-5-sonnet-20241022-v2:0`). See the [AWS Bedrock model IDs documentation](https://docs.aws.amazon.com/bedrock/latest/userguide/models-supported.html) for the full list of available models and their IDs. Model availability varies by AWS region. |

### IAM Policy Example

Here's a minimal IAM policy for Bedrock access using the Converse API:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "bedrock:Converse",
        "bedrock:ConverseStream"
      ],
      "Resource": "arn:aws:bedrock:*::foundation-model/*"
    }
  ]
}
```

For more restrictive access, limit the resource to specific models:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "bedrock:Converse",
        "bedrock:ConverseStream"
      ],
      "Resource": "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-5-sonnet-*"
    }
  ]
}
```

### Regional Considerations

- **Model Availability**: Not all models are available in all regions. Check AWS documentation for current availability
- **Latency**: Choose a region close to your Mattermost deployment for optimal performance
- **Data Residency**: Select regions that meet your data sovereignty requirements
- **Cost**: Pricing may vary by region

### Supported Features

AWS Bedrock through Mattermost Agents supports:

- Streaming responses for real-time interaction
- Tool/function calling for integrations
- Multi-modal capabilities (text and images) with compatible models
- Token usage tracking for cost management
- Custom endpoint URLs for VPC endpoints and proxy configurations
- Bearer token authentication for Bedrock console API keys

### Special Considerations

- **Authentication Priority**: The authentication method is selected in this order:
  1. IAM credentials (AWS Access Key ID + Secret Access Key fields in System Console)
  2. Bearer token (API Key field with base64 encoded Bedrock console key)
  3. Default credential chain (environment variables, IAM roles, etc.)
- **Model Enablement**: You must explicitly enable models in the AWS Bedrock console before using them
- **Quotas**: Be aware of AWS Bedrock service quotas and request increases if needed
- **Cold Starts**: First requests to a model may experience slightly higher latency
- **Cost Management**: Monitor usage through AWS Cost Explorer and consider setting up billing alerts
- **API Key Expiration**: Bedrock console API keys (base64 encoded) expire after 12 hours or when your console session ends. For production use, configure IAM user credentials (dedicated fields or API Key field format) or IAM roles for persistent authentication
- **VPC Endpoints**: If using AWS PrivateLink VPC endpoints, configure the VPC endpoint URL in the Custom Endpoint URL field
- **Proxy Support**: For proxy configurations, either configure the proxy at the Mattermost server level (environment variables) or use the Custom Endpoint URL field to point to a proxy endpoint

## Cohere

### Authentication

Obtain a [Cohere API key](https://dashboard.cohere.com/api-keys), then select **Cohere** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Cohere API key |
| **Default Model** | Yes | The model to use by default (see [Cohere's model documentation](https://docs.cohere.com/docs/models)) |

## Mistral

### Authentication

Obtain a [Mistral API key](https://console.mistral.ai/api-keys/), then select **Mistral** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Mistral API key |
| **Default Model** | Yes | The model to use by default (see [Mistral's model documentation](https://docs.mistral.ai/getting-started/models/)) |

## Azure OpenAI

### Authentication

For more details about integrating with Microsoft Azure's OpenAI services, see the [official Azure OpenAI documentation](https://learn.microsoft.com/en-us/azure/ai-services/openai/overview).

1. Provision sufficient [access to Azure OpenAI](https://learn.microsoft.com/en-us/azure/ai-services/openai/overview#how-do-i-get-access-to-azure-openai) for your organization and access your [Azure portal](https://portal.azure.com/)
2. If you do not already have one, deploy an Azure AI Hub resource within Azure AI Studio
3. Once the deployment is complete, navigate to the resource and select **Launch Azure AI Studio**
4. In the side navigation pane, select **Deployments** under **Shared resources**
5. Select **Deploy model** then **Deploy base model**
6. Select your desired model and select **Confirm**
7. Select **Deploy** to start your model
8. In Mattermost, select **OpenAI Compatible** in the **Service** dropdown
9. In the **Endpoint** panel for your new model deployment, copy the base URI of the **Target URI** (everything up to and including `.com`) and paste it in the **API URL** field in Mattermost
10. In the **Endpoint** panel for your new model deployment, copy the **Key** and paste it in the **API Key** field in Mattermost
11. In the **Deployment** panel for your new model deployment, copy the **Model name** and paste it in the **Default Model** field in Mattermost

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Azure OpenAI API key |
| **API URL** | Yes | Your Azure OpenAI endpoint |
| **Default Model** | Yes | The model to use by default (see [Azure OpenAI's model documentation](https://learn.microsoft.com/en-us/azure/ai-services/openai/concepts/models)) |
| **Send User ID** | No | Whether to send user IDs to Azure OpenAI |
