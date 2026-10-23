// Recipes for trusting Banda's workload identity tokens, filled in with this
// Banda's values. docs/configuration.md says which were verified end to end.

export type Values = {
	issuer: string;
	audience: string;
	/** The token file in thread pods. */
	path: string;
	/** host/path; empty binds no repository. */
	repo: string;
	/** The viewer's sign-in email: their tokens' sub is user:<email>. */
	email: string;
	/** The issuer's public keys as PEM, for verifiers that can't reach Banda. */
	pems: string[];
};

/** optional: an alternative, shown folded. */
export type Step = { title: string; code?: string; lang?: string; note?: string; optional?: boolean };
export type Recipe = { id: string; label: string; intro: string; steps: Step[] };

/** Where the recipes have threads write credential files that hold no secret but aren't the repo's business. */
const conf = '/home/banda/.config/banda';

/** Indented JSON, one-item arrays on one line. */
const json = (o: unknown) => JSON.stringify(o, null, 2).replace(/\[\n\s*("[^"\n]*")\n\s*\]/g, '[$1]');

/** s as one shell word: as is when it is plain, else single-quoted. */
export const sh = (s: string) => (/^[A-Za-z0-9@%+=:,./_-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`);

export function recipes(v: Values): Recipe[] {
	const host = v.issuer.replace(/^https?:\/\//, '');
	const sub = `user:${v.email}`;
	const slug = (v.email.split('@')[0] || 'me').toLowerCase().replace(/[^a-z0-9-]/g, '-');
	// Over plain http the vault trusts Banda's keys themselves, with discovery folded away (if Settings has the keys).
	const http = v.issuer.startsWith('http://') && v.pems.length > 0;
	const discovery: Step = {
		title: http ? 'Or trust its discovery URL: not over plain http' : 'Trust Banda',
		...(http && {
			optional: true,
			note: 'Over plain http anyone on the network path can hand the vault their own keys and sign in as anyone. Use this only once PUBLIC_URL is https.'
		}),
		lang: 'sh',
		code: `bao auth enable jwt\nbao write auth/jwt/config \\\n    oidc_discovery_url=${sh(v.issuer)} \\\n    bound_issuer=${sh(v.issuer)}`
	};
	const pubkeys: Step[] = v.pems.length
		? [
				{
					title: http ? 'Trust Banda’s keys' : 'Or, if the vault can’t reach Banda, trust its keys',
					optional: !http,
					note: 'Update these when Banda’s keys rotate (docs/deployment.md).',
					lang: 'sh',
					code:
						(http ? 'bao auth enable jwt\n' : '') +
						v.pems.map((p, i) => `cat > banda-${i + 1}.pem <<'EOF'\n${p}\nEOF`).join('\n') +
						`\nbao write auth/jwt/config \\\n    bound_issuer=${sh(v.issuer)} \\\n` +
						v.pems.map((_, i) => `    jwt_validation_pubkeys=@banda-${i + 1}.pem`).join(' \\\n')
				}
			]
		: [];
	return [
		{
			id: 'vault',
			label: 'OpenBao / Vault',
			intro: 'JWT auth with a role per team and a policy that gives each person their own path. With Vault, run vault instead of bao.',
			steps: [
				...(http ? [...pubkeys, discovery] : [discovery, ...pubkeys]),
				{
					title: 'A role for the team’s threads',
					lang: 'sh',
					code: `bao write auth/jwt/role/banda - <<'EOF'\n${json({
						role_type: 'jwt',
						bound_audiences: [v.audience],
						user_claim: 'email',
						bound_claims: { email_verified: true, ...(v.repo && { repo: v.repo }) },
						claim_mappings: { thread: 'thread', repo: 'repo', branch: 'branch' },
						token_policies: ['banda'],
						token_ttl: '15m'
					})}\nEOF`
				},
				{
					title: 'A policy: each person’s own secrets, and the team’s',
					lang: 'sh',
					code: `ACCESSOR=$(bao auth list -format=json | jq -r '."jwt/".accessor')\nbao policy write banda - <<EOF\npath "secret/data/users/{{identity.entity.aliases.$ACCESSOR.name}}/*" {\n  capabilities = ["read"]\n}\npath "secret/data/team/*" {\n  capabilities = ["read"]\n}\nEOF`
				},
				{
					title: 'Sign in from a thread',
					note: 'Set BAO_ADDR as a team secret; the agent can then fetch what it needs.',
					lang: 'sh',
					code: `export BAO_TOKEN=$(bao write -field=token auth/jwt/login role=banda jwt=@${sh(v.path)})\nbao kv get ${sh(`secret/users/${v.email}/db`)}`
				}
			]
		},
		{
			id: 'infisical',
			label: 'Infisical',
			intro: 'A machine identity with OIDC Auth (Organization › Access Control › Identities), added to the projects it may read.',
			steps: [
				{
					title: 'OIDC Auth settings',
					lang: 'text',
					code: `OIDC Discovery URL  ${v.issuer}\nIssuer              ${v.issuer}\nSubject             ${sub}\nAudiences           ${v.audience}${v.repo ? `\nClaims              repo = ${v.repo}` : ''}`,
					note: 'The subject is yours; one identity per person keeps everyone to their own projects.'
				},
				{
					title: 'Sign in from a thread',
					note: 'Set INFISICAL_IDENTITY_ID as a personal secret and INFISICAL_PROJECT_ID as a team secret.',
					lang: 'sh',
					code: `export INFISICAL_TOKEN=$(infisical login --method=oidc-auth \\\n    --machine-identity-id="$INFISICAL_IDENTITY_ID" --jwt="$(cat ${sh(v.path)})" --silent --plain)\ninfisical run --projectId="$INFISICAL_PROJECT_ID" -- npm test`
				}
			]
		},
		{
			id: 'aws',
			label: 'AWS',
			intro: 'An IAM OIDC provider for Banda and a role only your threads may assume, which reads your secrets and the team’s. The AWS SDKs and CLI then refresh credentials by themselves.',
			steps: [
				{
					title: 'Trust Banda and create your role',
					lang: 'sh',
					code: `ACCOUNT=$(aws sts get-caller-identity --query Account --output text)\naws iam create-open-id-connect-provider --url ${sh(v.issuer)} --client-id-list ${sh(v.audience)}\ncat > banda-trust.json <<'EOF'\n${json({
						Version: '2012-10-17',
						Statement: [
							{
								Effect: 'Allow',
								Principal: { Federated: `arn:aws:iam::ACCOUNT_ID:oidc-provider/${host}` },
								Action: 'sts:AssumeRoleWithWebIdentity',
								Condition: { StringEquals: { [`${host}:aud`]: v.audience, [`${host}:sub`]: sub } }
							}
						]
					})}\nEOF\nsed -i.bak "s/ACCOUNT_ID/$ACCOUNT/" banda-trust.json\naws iam create-role --role-name banda-${slug} \\\n    --assume-role-policy-document file://banda-trust.json\necho "AWS_ROLE_ARN=arn:aws:iam::$ACCOUNT:role/banda-${slug}"`
				},
				{
					title: 'What your role may read: your secrets and the team’s',
					note: 'An example; grant what your threads need and no more.',
					lang: 'sh',
					code: `cat > banda-secrets.json <<'EOF'\n${json({
						Version: '2012-10-17',
						Statement: [
							{
								Effect: 'Allow',
								Action: 'secretsmanager:GetSecretValue',
								Resource: [`arn:aws:secretsmanager:*:ACCOUNT_ID:secret:users/${v.email}/*`, 'arn:aws:secretsmanager:*:ACCOUNT_ID:secret:team/*']
							}
						]
					})}\nEOF\nsed -i.bak "s/ACCOUNT_ID/$ACCOUNT/" banda-secrets.json\naws iam put-role-policy --role-name banda-${slug} --policy-name secrets \\\n    --policy-document file://banda-secrets.json`
				},
				{
					title: 'Personal secrets in Settings',
					note: 'The role ARN is the one the first step printed. Not secret, but personal: the role is yours.',
					lang: 'text',
					code: `AWS_ROLE_ARN                 arn:aws:iam::<account>:role/banda-${slug}\nAWS_WEB_IDENTITY_TOKEN_FILE  ${v.path}`
				}
			]
		},
		{
			id: 'gcp',
			label: 'Google Cloud',
			intro: 'Workload Identity Federation: a pool that trusts Banda, access to single secrets for your subject, and a credential file that reads the token.',
			steps: [
				{
					title: 'Trust Banda',
					lang: 'sh',
					code: `PROJECT=$(gcloud config get-value project)\nNUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')\ngcloud iam workload-identity-pools create banda --location=global --display-name=Banda\ngcloud iam workload-identity-pools providers create-oidc banda \\\n    --location=global --workload-identity-pool=banda \\\n    --issuer-uri=${sh(v.issuer)} --allowed-audiences=${sh(v.audience)} \\\n    --attribute-mapping=google.subject=assertion.sub,attribute.repo=assertion.repo,attribute.email=assertion.email \\\n    --attribute-condition=${sh(`assertion.email_verified == true${v.repo ? ` && assertion.repo == "${v.repo}"` : ''}`)}`
				},
				{
					title: 'Let your threads read single secrets: yours, and one of the team’s',
					note: 'A binding per secret, not on the project: a project-wide one would read every secret in it.',
					lang: 'sh',
					code: `gcloud secrets add-iam-policy-binding ${sh(`${slug}-db`)} --role=roles/secretmanager.secretAccessor \\\n    --member="principal://iam.googleapis.com/projects/$NUMBER/locations/global/workloadIdentityPools/banda/subject/"${sh(sub)}\ngcloud secrets add-iam-policy-binding team-db --role=roles/secretmanager.secretAccessor \\\n    --member="principalSet://iam.googleapis.com/projects/$NUMBER/locations/global/workloadIdentityPools/banda/*"`
				},
				{
					title: 'A credential file that reads the token',
					note: `It holds no secret, but keep it out of the repository: save its content as the team secret GOOGLE_CREDENTIAL_CONFIG, and set GOOGLE_APPLICATION_CREDENTIALS to ${conf}/gcp.json as a team secret.`,
					lang: 'sh',
					code: `gcloud iam workload-identity-pools create-cred-config \\\n    "projects/$NUMBER/locations/global/workloadIdentityPools/banda/providers/banda" \\\n    --credential-source-file=${sh(v.path)} --output-file=gcp.json\ncat gcp.json`
				},
				{
					title: 'Write it in each thread, in .banda/setup.sh',
					note: 'The script runs once per thread before the agent starts (and again when it changes); it holds no secret either.',
					lang: 'sh',
					code: `mkdir -p ${conf}\nprintf '%s' "$GOOGLE_CREDENTIAL_CONFIG" > ${conf}/gcp.json`
				}
			]
		},
		{
			id: 'kubernetes',
			label: 'Kubernetes',
			intro: 'The API server trusts Banda through structured authentication (Kubernetes 1.30 and later); RBAC decides what each person may do.',
			steps: [
				{
					title: 'Authentication config (--authentication-config)',
					lang: 'yaml',
					code: `apiVersion: apiserver.config.k8s.io/v1beta1\nkind: AuthenticationConfiguration\njwt:\n  - issuer:\n      url: ${JSON.stringify(v.issuer)}\n      audiences: [${JSON.stringify(v.audience)}]\n    claimMappings:\n      username:\n        claim: email\n        prefix: "banda:"\n    claimValidationRules:\n      - expression: "claims.email_verified == true"\n        message: the email must be verified${v.repo ? `\n      - expression: ${JSON.stringify(`claims.repo == '${v.repo}'`)}\n        message: ${JSON.stringify(`only threads on ${v.repo}`)}` : ''}`
				},
				{
					title: 'What you may do',
					lang: 'sh',
					code: `kubectl create clusterrolebinding banda-${slug}-view --clusterrole=view --user=${sh(`banda:${v.email}`)}`
				},
				{
					title: 'A kubeconfig that reads the token',
					note: `It holds no secret, but keep it out of the repository: save its content as the team secret BANDA_KUBECONFIG, and set KUBECONFIG to ${conf}/kubeconfig as a team secret.`,
					lang: 'sh',
					code: `SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')\nCA=$(kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')\ncat <<EOF\napiVersion: v1\nkind: Config\nclusters:\n  - name: cluster\n    cluster: {server: $SERVER, certificate-authority-data: $CA}\nusers:\n  - name: banda\n    user: {tokenFile: ${JSON.stringify(v.path)}}\ncontexts:\n  - name: banda\n    context: {cluster: cluster, user: banda}\ncurrent-context: banda\nEOF`
				},
				{
					title: 'Write it in each thread, in .banda/setup.sh',
					note: 'The script runs once per thread before the agent starts (and again when it changes); it holds no secret either.',
					lang: 'sh',
					code: `mkdir -p ${conf}\nprintf '%s' "$BANDA_KUBECONFIG" > ${conf}/kubeconfig`
				}
			]
		}
	];
}

/** Splits code into runs, marking the values Banda filled in. */
export function marked(code: string, values: string[]): { text: string; filled: boolean }[] {
	const vs = [...new Set(values.filter((v) => v.length > 2))].sort((a, b) => b.length - a.length);
	if (!vs.length) return [{ text: code, filled: false }];
	const re = new RegExp(`(${vs.map((v) => v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'g');
	return code
		.split(re)
		.filter(Boolean)
		.map((text) => ({ text, filled: vs.includes(text) }));
}

/** The JWKS's keys as PEM public keys (SPKI), through WebCrypto. */
export async function jwksToPem(jwks: { keys: JsonWebKey[] }): Promise<string[]> {
	return Promise.all(
		jwks.keys.map(async (k) => {
			const key = await crypto.subtle.importKey('jwk', { kty: k.kty, n: k.n, e: k.e }, { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' }, true, ['verify']);
			const der = btoa(String.fromCharCode(...new Uint8Array(await crypto.subtle.exportKey('spki', key))));
			return `-----BEGIN PUBLIC KEY-----\n${der.match(/.{1,64}/g)!.join('\n')}\n-----END PUBLIC KEY-----`;
		})
	);
}
