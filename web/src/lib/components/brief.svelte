<script lang="ts">
	import { api } from '#lib/api.js';
	import type { BriefItem, BriefResponse } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads, userName } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	// What happened since my last visit. Loading it is seeing it: the next visit starts from here.
	let brief = $state<BriefResponse>();
	let more = $state<Record<string, boolean>>({});

	$effect(() => {
		api.brief({}).then((b) => {
			brief = b;
			api.briefSeen({ until: b.until });
		});
	});

	const me = $derived(threads.me?.id ?? '');
	const sections = $derived(
		brief
			? ([
					['Waiting on you', brief.waiting],
					['Finished', brief.finished],
					['Sub-threads started', brief.spawned],
					['Settled', brief.settled]
				] as [string, BriefItem[]][]).filter(([, list]) => list.length)
			: []
	);
</script>

{#if brief}
	<section aria-label="Brief" class="border-b px-4 py-3">
		<h2 class="text-sm font-medium">
			Since your last visit <span class="font-normal text-muted-foreground">{ago(brief.since, threads.now) === 'now' ? 'just now' : `${ago(brief.since, threads.now)} ago`}</span>
		</h2>
		{#if !sections.length}
			<p class="mt-1 text-xs text-muted-foreground">Nothing new.</p>
		{/if}
		<div class="mt-2 grid grid-cols-[repeat(auto-fit,minmax(min(20rem,100%),1fr))] gap-x-6 gap-y-3">
			{#each sections as [name, list] (name)}
				<section aria-label={name} class="min-w-0">
					<h3 class="text-xs font-medium text-muted-foreground">
						{name} <span class="tabular-nums">{list.length}</span>
					</h3>
					<ul class="mt-1 space-y-0.5">
						{#each more[name] ? list : list.slice(0, 5) as i (i.threadId)}
							<li>
								<a href="/t/{i.threadId}" class="flex items-baseline gap-1.5 rounded px-1 py-0.5 text-sm hover:bg-accent" title={i.text}>
									{#if i.ownerId !== me}<span class="shrink-0 text-xs text-muted-foreground">{userName(i.ownerId)}</span>{/if}
									<span class="shrink-0 truncate font-medium md:max-w-[50%]">{i.title || 'Untitled thread'}</span>
									<span class="min-w-0 flex-1 truncate text-xs text-muted-foreground">{i.text}</span>
									<span class="shrink-0 text-[11px] text-muted-foreground tabular-nums">{ago(i.time, threads.now)}</span>
								</a>
							</li>
						{/each}
					</ul>
					{#if list.length > 5 && !more[name]}
						<button class="hit px-1 text-xs text-muted-foreground hover:text-foreground" onclick={() => (more[name] = true)}>
							{list.length - 5} more
						</button>
					{/if}
				</section>
			{/each}
		</div>
	</section>
{/if}
