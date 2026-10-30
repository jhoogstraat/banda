<script lang="ts">
	import { replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { timestampFromDate } from '@bufbuild/protobuf/wkt';
	import { ConnectError } from '@connectrpc/connect';
	import SearchIcon from '@lucide/svelte/icons/search';
	import PageHeader from '#lib/components/page-header.svelte';
	import SearchHit from '#lib/components/search-hit.svelte';
	import { Input } from '#lib/components/ui/input/index.js';
	import { NativeSelect, NativeSelectOption } from '#lib/components/ui/native-select/index.js';
	import { api } from '#lib/api.js';
	import { SearchKind, type SearchResult } from '#lib/gen/banda/v1/banda_pb.js';
	import { noRepo, projectName, projects, threads } from '#lib/threads.svelte.js';

	// Everything the team and its agents said, searchable; the filters live in the URL so a search can be shared.
	const p = page.url.searchParams;
	const f = $state({
		q: p.get('q') ?? '',
		actor: p.get('actor') ?? '',
		repo: p.get('repo') ?? '',
		kind: p.get('kind') ?? '',
		after: p.get('after') ?? '',
		before: p.get('before') ?? ''
	});
	const kinds: Record<string, SearchKind> = { people: SearchKind.PEOPLE, agent: SearchKind.AGENT, code: SearchKind.CODE };
	let results = $state<SearchResult[]>();
	let error = $state('');
	let asked = 0;

	// A day picked in a date input, as the start of that local day (`plus` days later).
	const day = (d: string, plus = 0) => {
		if (!d) return undefined;
		const t = new Date(`${d}T00:00`);
		t.setDate(t.getDate() + plus);
		return timestampFromDate(t);
	};

	$effect(() => {
		const { q, actor, repo, kind, after, before } = f;
		const n = ++asked;
		const timer = setTimeout(async () => {
			replaceState(`?${new URLSearchParams(Object.entries(f).filter(([, v]) => v))}`, {});
			if (!q.trim()) return (results = undefined);
			try {
				const repoUrl = repo === noRepo ? '' : repo || undefined; // unset is any project
				const res = await api.search({ query: q, actor, repoUrl, kind: kinds[kind], after: day(after), before: day(before, 1), limit: 50 });
				if (n === asked) [results, error] = [res.results, ''];
			} catch (e) {
				if (n === asked) error = ConnectError.from(e).rawMessage;
			}
		}, 200);
		return () => clearTimeout(timer);
	});
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader><h1 class="text-sm font-medium">Search</h1></PageHeader>
	<div class="min-h-0 flex-1 overflow-y-auto">
		<div class="mx-auto w-full max-w-3xl space-y-4 p-4">
			<div class="relative">
				<SearchIcon class="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
				<!-- svelte-ignore a11y_autofocus -->
				<Input bind:value={f.q} autofocus placeholder="Search messages, agent replies, commands and files" class="h-10 pl-9" aria-label="Search" />
			</div>
			<div class="flex flex-wrap items-center gap-2 text-sm">
				<NativeSelect bind:value={f.actor} size="sm" aria-label="Person">
					<NativeSelectOption value="">Anyone</NativeSelectOption>
					<NativeSelectOption value="agent">Agents</NativeSelectOption>
					{#each threads.users as u (u.id)}<NativeSelectOption value={u.id}>{u.name}</NativeSelectOption>{/each}
				</NativeSelect>
				<NativeSelect bind:value={f.kind} size="sm" aria-label="Kind">
					<NativeSelectOption value="">Everything</NativeSelectOption>
					<NativeSelectOption value="people">Messages</NativeSelectOption>
					<NativeSelectOption value="agent">Agent replies</NativeSelectOption>
					<NativeSelectOption value="code">Tool calls and reviews</NativeSelectOption>
				</NativeSelect>
				<NativeSelect bind:value={f.repo} size="sm" aria-label="Project">
					<NativeSelectOption value="">All projects</NativeSelectOption>
					{#each new Set([...projects(), ...(f.repo ? [f.repo] : [])]) as url (url)}<NativeSelectOption value={url}>{projectName(url)}</NativeSelectOption>{/each}
				</NativeSelect>
				<!-- The range wraps as one. -->
				<div class="flex items-center gap-1.5 text-muted-foreground">
					<label class="flex items-center gap-1.5">From <Input type="date" bind:value={f.after} class="h-8 w-auto" /></label>
					<label class="flex items-center gap-1.5">to <Input type="date" bind:value={f.before} class="h-8 w-auto" /></label>
				</div>
			</div>

			{#if error}
				<p class="text-sm text-destructive">{error}</p>
			{:else if results?.length}
				<ul class="divide-y rounded-lg border">
					{#each results as hit (`${hit.threadId}#${hit.seq}`)}
						<!-- The row opens the message (its link covers the row); the author's name on top opens their page. -->
						<li class="relative flex px-4 py-3 hover:bg-muted/50">
							<a href="/t/{hit.threadId}#{hit.seq}" class="absolute inset-0" aria-label="Open in {hit.threadTitle || 'Untitled thread'}"></a>
							<SearchHit {hit} people />
						</li>
					{/each}
				</ul>
			{:else if results}
				<p class="py-10 text-center text-sm text-muted-foreground">Nothing found.</p>
			{:else}
				<p class="py-10 text-center text-sm text-muted-foreground">Search everything said in every thread: by people, by agents, and what the agents ran.</p>
			{/if}
		</div>
	</div>
</div>
