<script lang="ts">
	// The button for what a forge error asks of the person (connect GitHub, install the app, add a secret): errors
	// name the action, this makes it one click. On a thread, connecting comes back to it; once connected (here or
	// elsewhere), the button says so instead of asking again.
	import CheckIcon from '@lucide/svelte/icons/check';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import { Button } from '#lib/components/ui/button/index.js';
	import { forgeAction, github, loadGitHub } from '#lib/github.svelte.js';
	import { gitlab, loadGitLab } from '#lib/gitlab.svelte.js';

	let { message, thread, size = 'xs' }: { message: string; thread?: { id: string; repoUrl: string }; size?: 'xs' | 'sm' } = $props();
	const action = $derived(forgeAction(message, thread));
	$effect(() => {
		if (action?.connects === 'github') loadGitHub();
		else if (action?.connects === 'gitlab') loadGitLab();
	});
	const conn = $derived(
		action?.connects === 'github'
			? github.res?.connection
			: action?.connects === 'gitlab'
				? gitlab.res?.hosts.find((h) => h.host === action.host)?.connection
				: undefined
	);
</script>

{#if conn && !conn.broken}
	<span class="inline-flex items-center gap-1 text-xs text-muted-foreground"><CheckIcon class="size-3.5" /> Connected as @{conn.login}</span>
{:else if action}
	<Button {size} variant="outline" href={action.href} target={action.external ? '_blank' : undefined} rel={action.external ? 'noreferrer' : undefined}
		data-sveltekit-reload={action.href.startsWith('/github/') || action.href.startsWith('/gitlab/') ? true : undefined}>
		{action.label}
		{#if action.external}<ExternalLinkIcon />{/if}
	</Button>
{/if}
