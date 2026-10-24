-- Teammates run the agent only where its owner is on an API key (Anthropic's
-- terms): trust set before that rule goes off on a subscription. Requests
-- still open there can only be declined.
UPDATE threads SET trusted = false WHERE trusted AND owner_id NOT IN
  (SELECT user_id FROM harness_settings WHERE harness = 'claude' AND auth_method = 'api_key');
