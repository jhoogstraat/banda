// How pull requests (GitLab: merge requests) look everywhere: the header chip, sidebar rows, team cards,
// the chat line and the Pull request tab. Colours follow the forges' own (open green, draft grey, merged violet,
// closed red); checks and reviews add a glyph only when they say something.
import type { Component } from 'svelte';
import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
import CircleDashedIcon from '@lucide/svelte/icons/circle-dashed';
import CircleDotIcon from '@lucide/svelte/icons/circle-dot';
import CircleXIcon from '@lucide/svelte/icons/circle-x';
import GitMergeIcon from '@lucide/svelte/icons/git-merge';
import GitPullRequestIcon from '@lucide/svelte/icons/git-pull-request';
import GitPullRequestClosedIcon from '@lucide/svelte/icons/git-pull-request-closed';
import GitPullRequestDraftIcon from '@lucide/svelte/icons/git-pull-request-draft';
import {
	ChecksState,
	Mergeable,
	PullState,
	ReviewState,
	type PullRequestCheck,
	type PullRequestChanged,
	type PullRequestSummary
} from './gen/banda/v1/banda_pb.js';

type Look = { label: string; icon: Component; class: string };

export const pullStates: Record<PullState, Look> = {
	[PullState.UNSPECIFIED]: { label: 'Open', icon: GitPullRequestIcon, class: 'text-emerald-600 dark:text-emerald-400' },
	[PullState.OPEN]: { label: 'Open', icon: GitPullRequestIcon, class: 'text-emerald-600 dark:text-emerald-400' },
	[PullState.DRAFT]: { label: 'Draft', icon: GitPullRequestDraftIcon, class: 'text-zinc-500 dark:text-zinc-400' },
	[PullState.MERGED]: { label: 'Merged', icon: GitMergeIcon, class: 'text-violet-600 dark:text-violet-400' },
	[PullState.CLOSED]: { label: 'Closed', icon: GitPullRequestClosedIcon, class: 'text-red-600 dark:text-red-400' }
};

export const checkStates: Record<ChecksState, Look> = {
	[ChecksState.UNSPECIFIED]: { label: 'No checks', icon: CircleDashedIcon, class: 'text-muted-foreground' },
	[ChecksState.PENDING]: { label: 'Running', icon: CircleDotIcon, class: 'text-amber-500' },
	[ChecksState.PASSING]: { label: 'Passed', icon: CircleCheckIcon, class: 'text-emerald-600 dark:text-emerald-400' },
	[ChecksState.FAILING]: { label: 'Failed', icon: CircleXIcon, class: 'text-destructive' },
	[ChecksState.SKIPPED]: { label: 'Skipped', icon: CircleDashedIcon, class: 'text-muted-foreground' }
};

export const reviewLabels: Record<ReviewState, string> = {
	[ReviewState.UNSPECIFIED]: '',
	[ReviewState.REQUESTED]: 'Review requested',
	[ReviewState.APPROVED]: 'Approved',
	[ReviewState.CHANGES_REQUESTED]: 'Changes requested',
	[ReviewState.COMMENTED]: 'Commented'
};

/** "pull request" or "merge request", and its short form and number prefix. */
export const noun = (forge: string) => (forge === 'gitlab' ? 'merge request' : 'pull request');
export const short = (forge: string) => (forge === 'gitlab' ? 'MR' : 'PR');
export const ref = (p: PullRequestSummary) => `${p.forge === 'gitlab' ? '!' : '#'}${p.number}`;

/** Open or draft: what can still change. */
export const live = (p: PullRequestSummary) => p.state === PullState.OPEN || p.state === PullState.DRAFT;

/** One line for a tooltip or label: "PR #12 · Open · checks failed · approved · conflicts". */
export function describe(p: PullRequestSummary): string {
	const parts = [`${short(p.forge)} ${ref(p)}`, pullStates[p.state].label];
	if (live(p)) {
		if (p.checks === ChecksState.FAILING) parts.push('checks failed');
		else if (p.checks === ChecksState.PENDING) parts.push('checks running');
		else if (p.checks === ChecksState.PASSING) parts.push('checks passed');
		if (reviewLabels[p.review]) parts.push(reviewLabels[p.review].toLowerCase());
		if (p.mergeable === Mergeable.CONFLICTS) parts.push('conflicts');
	}
	return parts.join(' · ');
}

/** "2 of 5 failing", "1 of 3 running", "All 4 passed", "No checks reported". */
export function checksSummary(checks: PullRequestCheck[]): string {
	if (!checks.length) return 'No checks reported';
	const n = (s: ChecksState) => checks.filter((c) => c.state === s).length;
	if (n(ChecksState.FAILING)) return `${n(ChecksState.FAILING)} of ${checks.length} failing`;
	if (n(ChecksState.PENDING)) return `${n(ChecksState.PENDING)} of ${checks.length} running`;
	return n(ChecksState.SKIPPED) ? `${n(ChecksState.PASSING)} passed, ${n(ChecksState.SKIPPED)} skipped` : `All ${checks.length} passed`;
}

/** The chat line for a change, by a person (`by`) or the forge (empty): "Alice opened pull request #12", "Checks failed on pull request #12". */
export function changeText(c: PullRequestChanged, by = ''): string {
	const p = c.pullRequest!;
	const it = `${noun(p.forge)} ${ref(p)}`;
	const It = it[0].toUpperCase() + it.slice(1);
	const byWhom = by ? ` by ${by}` : '';
	switch (c.change) {
		case 'opened':
			return `${by || 'Someone'} opened ${it}`;
		case 'linked':
			return by ? `${by} linked ${it}` : `${It} on this branch was linked`;
		case 'ready':
			return `${It} was marked ready for review${byWhom}`;
		case 'draft':
			return `${It} was made a draft${byWhom}`;
		case 'checks_failed':
			return `Checks failed on ${it}`;
		case 'checks_passed':
			return `Checks pass again on ${it}`;
		case 'approved':
			return `${It} was approved`;
		case 'changes_requested':
			return `Changes requested on ${it}`;
		case 'merged':
			return `${It} was merged${byWhom}`;
		case 'closed':
			return `${It} was closed${byWhom}`;
		case 'reopened':
			return `${It} was reopened${byWhom}`;
	}
	return `${It} changed`;
}

/** "3m 20s" between two dates, or since the start while running. */
export function duration(start?: Date, end?: Date): string {
	if (!start) return '';
	const s = Math.max(0, Math.round(((end ?? new Date()).getTime() - start.getTime()) / 1000));
	return s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m ${s % 60}s` : `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}
