import BotIcon from '@lucide/svelte/icons/bot';
import FilePenIcon from '@lucide/svelte/icons/file-pen';
import FilePlusIcon from '@lucide/svelte/icons/file-plus';
import FileTextIcon from '@lucide/svelte/icons/file-text';
import GlobeIcon from '@lucide/svelte/icons/globe';
import ListTodoIcon from '@lucide/svelte/icons/list-todo';
import SearchIcon from '@lucide/svelte/icons/search';
import TerminalIcon from '@lucide/svelte/icons/terminal';
import WrenchIcon from '@lucide/svelte/icons/wrench';
import { todos, type Tool } from './events.ts';

export const icons: Record<string, typeof WrenchIcon> = {
	Bash: TerminalIcon,
	Read: FileTextIcon,
	Write: FilePlusIcon,
	Edit: FilePenIcon,
	MultiEdit: FilePenIcon,
	Grep: SearchIcon,
	Glob: SearchIcon,
	WebFetch: GlobeIcon,
	WebSearch: GlobeIcon,
	Task: BotIcon,
	Agent: BotIcon,
	TodoWrite: ListTodoIcon
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function input(tool: Tool): Record<string, any> {
	try {
		const v = JSON.parse(tool.call.inputJson);
		return v && typeof v === 'object' ? v : {};
	} catch {
		return {};
	}
}

// Paths relative to the checkout (the runner's ~/work).
export const rel = (p = '') => p.replace(/^\/home\/banda\/work\//, '');
export const lines = (s = '') => (s ? s.replace(/\n$/, '').split('\n') : []);
export const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

// Bash reports a non-zero exit as an error result starting with "Exit code N".
export const exitCode = (tool: Tool) =>
	tool.result ? (tool.result.isError ? Number(/^Exit code (\d+)/.exec(tool.result.output)?.[1] ?? 1) : 0) : undefined;

// Unified lines for an edit: the common head and tail of old and new are context.
function hunk(a: string, b: string) {
	const x = lines(a),
		y = lines(b);
	let p = 0;
	while (p < x.length && p < y.length && x[p] === y[p]) p++;
	let s = 0;
	while (s < x.length - p && s < y.length - p && x[x.length - 1 - s] === y[y.length - 1 - s]) s++;
	return [
		...x.slice(0, p).map((l) => ' ' + l),
		...x.slice(p, x.length - s).map((l) => '-' + l),
		...y.slice(p, y.length - s).map((l) => '+' + l),
		...x.slice(x.length - s).map((l) => ' ' + l)
	];
}

// The diff an Edit, MultiEdit or Write makes, or undefined for other tools.
export function diff(tool: Tool) {
	const i = input(tool);
	const edits =
		tool.call.name === 'Write'
			? [{ old_string: '', new_string: i.content }]
			: tool.call.name === 'MultiEdit'
				? (i.edits ?? [])
				: tool.call.name === 'Edit'
					? [i]
					: undefined;
	return edits?.flatMap((e: { old_string?: string; new_string?: string }, n: number) => [
		...(n ? ['@@'] : []),
		...hunk(e.old_string ?? '', e.new_string ?? '')
	]) as string[] | undefined;
}

// How many matches a Grep or Glob found, from its text output.
function matches(tool: Tool) {
	const out = tool.result?.output ?? '';
	const found = /^Found (\d+)/.exec(out);
	if (found) return Number(found[1]);
	return /^No (files|matches) found/.test(out) ? 0 : lines(out).filter(Boolean).length;
}

// One line about a call: a label, its most telling argument, and a short fact about the result.
export function describe(tool: Tool): { label: string; detail: string; meta: string } {
	const i = input(tool);
	const r = tool.result;
	switch (tool.call.name) {
		case 'Bash':
			return { label: '', detail: `$ ${i.command ?? ''}`, meta: '' };
		case 'Edit':
		case 'MultiEdit':
		case 'Write': {
			const d = diff(tool) ?? [];
			const add = d.filter((l) => l[0] === '+').length;
			const del = d.filter((l) => l[0] === '-').length;
			return { label: tool.call.name, detail: rel(i.file_path), meta: `+${add} −${del}` };
		}
		case 'Read':
			return {
				label: 'Read',
				detail: rel(i.file_path) + (i.offset ? `:${i.offset}` : ''),
				meta: r && !r.isError ? plural(lines(r.output).length, 'line') : ''
			};
		case 'Grep':
		case 'Glob':
			return {
				label: tool.call.name,
				detail: (i.pattern ?? '') + (i.path ? ` in ${rel(i.path)}` : ''),
				meta: r && !r.isError ? plural(matches(tool), tool.call.name === 'Glob' || !i.output_mode || i.output_mode === 'files_with_matches' ? 'file' : 'match') : ''
			};
		case 'WebFetch':
			return { label: 'Fetch', detail: i.url ?? '', meta: '' };
		case 'WebSearch':
			return { label: 'Search', detail: i.query ?? '', meta: '' };
		case 'Task':
		case 'Agent': {
			const last = tool.children.at(-1);
			const now = last && !r ? describe(last) : undefined;
			return {
				label: i.description || 'Subagent',
				detail: now ? `${now.label} ${now.detail}`.trim() : (i.subagent_type ?? ''),
				meta: tool.children.length ? plural(tool.children.length, 'tool') : ''
			};
		}
		case 'TodoWrite': {
			const t = todos(tool.call);
			return {
				label: 'Plan',
				detail: t.find((x) => x.status === 'in_progress')?.activeForm ?? '',
				meta: `${t.filter((x) => x.status === 'completed').length}/${t.length} done`
			};
		}
	}
	return {
		label: tool.call.name,
		detail: i.file_path ?? i.command ?? i.pattern ?? i.url ?? i.query ?? i.path ?? i.description ?? '',
		meta: ''
	};
}
