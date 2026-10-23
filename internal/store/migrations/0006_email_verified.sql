-- Whether the identity provider vouched for the user's email at their last
-- sign-in: email_verified false is unverified; a provider that omits the
-- claim (Entra ID) is trusted, as Kubernetes does. Workload identity tokens
-- name the email, so an unverified one gets none.
ALTER TABLE users ADD COLUMN email_verified boolean NOT NULL DEFAULT true;
