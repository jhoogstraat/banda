<script lang="ts">
	import { page } from '$app/state';
	import { Button } from '#lib/components/ui/button/index.js';

	// Shown without a session, so it calls nothing that needs one (see +layout.svelte).
	const q = page.url.searchParams;
	const next = q.get('next') || '/';
	const signedOut = q.has('signed-out');
	let provider = $state(''); // until /auth/provider answers
	fetch('/auth/provider')
		.then(async (r) => r.ok && r.headers.get('Content-Type')?.startsWith('text/plain') && (provider = await r.text()))
		.catch(() => {});

	// The reasons /auth/callback sends (internal/api/oidc.go).
	const errors: Record<string, (p: string) => string> = {
		state: () => 'The sign-in expired or was started in another tab. Try again.',
		denied: (p) => `${p} didn’t sign you in: it was cancelled or refused.`,
		failed: (p) => `Banda couldn’t complete the sign-in with ${p}. Try again; if it keeps failing, the API log says why.`
	};
	const error = q.get('error');
</script>

<svelte:head>
	<title>Sign in · Banda</title>
</svelte:head>

<main class="grid min-h-dvh place-items-center bg-background p-6">
	<div class="flex w-full max-w-sm flex-col gap-8">
		<div class="flex flex-col gap-2">
			<h1 class="text-3xl font-semibold tracking-tight">Banda</h1>
			<p class="text-sm text-muted-foreground">
				Where the team builds software together with coding agents: one thread, one agent and one sandbox each, open to everyone.
			</p>
		</div>
		<div class="flex flex-col gap-3">
			{#if signedOut}
				<p role="status" class="text-sm">You signed out.</p>
			{:else if error}
				<p role="alert" class="text-sm text-destructive">{(errors[error] ?? errors.failed)(provider || 'the provider')}</p>
			{/if}
			<Button href="/auth/login?next={encodeURIComponent(next)}" data-sveltekit-reload size="lg" class="w-full">
				Sign in{provider && ` with ${provider}`}
			</Button>
		</div>
	</div>
</main>
