# Configure Agents with AWS Bedrock

Use Amazon Bedrock with Agents: enable model access, create an IAM policy and EC2 instance profile, and add the service with no stored credentials.

The recommended setup runs Mattermost on EC2 with an IAM instance profile. For other credential options and the service fields, see [Provider configuration](providers.md#aws-bedrock).

## Before you begin

- Mattermost Server v12.0.0 or later with the Agents plugin enabled.
- An AWS account with permission to create IAM resources and enable Bedrock models.
- For the instance profile approach, Mattermost running on EC2. Attach the profile to every Mattermost app server.

## Enable model access

**Anthropic Claude models** need a one-time use case submission (the First Time Use form) per AWS account, or once on the management account if you use AWS Organizations. In the [Bedrock model catalog](https://console.aws.amazon.com/bedrock/home#/model-catalog), select **Submit use case details** and fill in the form. Or use the CLI:

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

**Other models** (Amazon Nova and Titan, Meta Llama, Mistral): in the Bedrock console, go to **Model access**, select the models, and choose **Request model access**. Most don't need the form.

### Use inference profile IDs for newer Claude models

Newer Claude models can't be invoked by their base model ID. Using one fails with `ValidationException: Invocation of model ID ... with on-demand throughput isn't supported`. Put a cross-Region inference profile ID in **Default model** instead:

| Model | Inference profile ID |
|---|---|
| Claude Sonnet 4.6, US routing | `us.anthropic.claude-sonnet-4-6` |
| Claude Sonnet 4.6, global routing | `global.anthropic.claude-sonnet-4-6` |

## Create the IAM resources

Create a policy that allows model invocation:

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

Replace `<YOUR_ACCOUNT_ID>` with your 12-digit account ID. Bedrock's Converse API uses these two actions. `bedrock:Converse` isn't a valid IAM action. The `inference-profile/*` resource is required for Claude models. To limit access to specific models, replace the wildcards with model ARNs. For Claude you need both a `foundation-model` ARN and an `inference-profile` ARN, for example `arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-sonnet-4-6` and `arn:aws:bedrock:us-east-1:<YOUR_ACCOUNT_ID>:inference-profile/us.anthropic.claude-sonnet-4-6`.

Create a role EC2 can assume, attach the policy, and create an instance profile:

```bash
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

aws iam attach-role-policy \
  --role-name MattermostBedrockRole \
  --policy-arn arn:aws:iam::<YOUR_ACCOUNT_ID>:policy/MattermostBedrockAccess

aws iam create-instance-profile --instance-profile-name MattermostBedrockProfile

aws iam add-role-to-instance-profile \
  --instance-profile-name MattermostBedrockProfile \
  --role-name MattermostBedrockRole
```

Attach the profile to each running Mattermost instance, or set it in your launch template or infrastructure code for new ones:

```bash
aws ec2 associate-iam-instance-profile \
  --iam-instance-profile Name=MattermostBedrockProfile \
  --instance-id <YOUR_INSTANCE_ID>
```

## Add the service and an agent

1. Go to **System Console > Plugins > Agents** and select **Add an AI Service**.
2. Set **Service type** to **AWS Bedrock**, **AWS Region** to a region where Bedrock is available, and **Default model** to the model or inference profile ID.
3. Leave **AWS Access Key ID**, **AWS Secret Access Key**, and **API Key** empty so the plugin uses the instance profile. Select **Save**.
4. Create an agent that uses this service. See [Create and manage agents](managing_agents.md).

Credentials are resolved in this order: AWS Access Key ID and Secret Access Key, then API Key, then the default AWS credential chain. A Bedrock console API key is for short-term testing only, because it expires after 12 hours.

## Verify

1. On an app server, confirm the role is attached. If your instances require IMDSv2:

   ```bash
   TOKEN=$(curl -s -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 60")
   curl -s -H "X-aws-ec2-metadata-token: $TOKEN" http://169.254.169.254/latest/meta-data/iam/security-credentials/
   ```

   It should print `MattermostBedrockRole`.
2. Send the agent a direct message. It should answer with the Bedrock model.
3. If it doesn't, check the server log:

   ```bash
   tail -f /opt/mattermost/logs/mattermost.log | grep -i "bedrock\|mattermost-ai"
   ```

## Troubleshooting

| Error | Fix |
|---|---|
| `AccessDeniedException: not authorized to perform bedrock:InvokeModelWithResponseStream` | The policy must include `bedrock:InvokeModel` and `bedrock:InvokeModelWithResponseStream`, and the `inference-profile/*` ARN if you use Claude. |
| `ValidationException: Invocation of model ID ... with on-demand throughput isn't supported` | Use the inference profile ID, such as `us.anthropic.claude-sonnet-4-6`, not the base model ID. |
| `ThrottlingException: Too many tokens per day` | Your account may have a zero on-demand quota for the model. Check [Service Quotas](https://docs.aws.amazon.com/bedrock/latest/userguide/quotas.html) in that region. New accounts might need a support ticket or a different region. |
| `failed to load AWS config` | The instance profile isn't attached. Check the metadata request above. |
| "An error occurred while accessing the LLM" | See the server log. Common causes are a wrong model ID, a model not available in the region, or missing IAM permissions. |
