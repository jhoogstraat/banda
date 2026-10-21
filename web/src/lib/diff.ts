import type { ReviewComment } from './gen/banda/v1/banda_pb.js';

// A diff line numbered in the base (`old`) and the branch (`new`) version of its file.
// Hunk headers and "\ No newline" lines have neither.
export type Line = { text: string; old?: number; new?: number };
export type DiffFile = { path: string; lines: Line[]; add: number; del: number };
export type Comment = Omit<ReviewComment, '$typeName' | '$unknown'>;

export function parseDiff(diff: string): DiffFile[] {
	return diff
		.split(/^(?=diff --git )/m)
		.filter(Boolean)
		.map((chunk) => {
			const raw = chunk.replace(/\n$/, '').split('\n');
			const hunk = raw.findIndex((l) => l.startsWith('@@'));
			let [o, n, add, del] = [0, 0, 0, 0];
			// Without hunks (binary, mode change) show the header lines instead.
			const lines = raw.slice(hunk < 0 ? 1 : hunk).map((text): Line => {
				const h = /^@@ -(\d+)(?:,\d+)? \+(\d+)/.exec(text);
				if (h) [o, n] = [+h[1], +h[2]];
				if (h || hunk < 0 || text[0] === '\\') return { text };
				if (text[0] === '+') return add++, { text, new: n++ };
				if (text[0] === '-') return del++, { text, old: o++ };
				return { text, old: o++, new: n++ };
			});
			return { path: /^diff --git a\/.* b\/(.*)$/.exec(raw[0])?.[1] ?? raw[0], lines, add, del };
		});
}

// The diff lines from..to as a comment's location: numbered in the branch's file, or in the base's if all were removed.
export function locate(lines: Line[], from: number, to: number) {
	const sel = lines.slice(from, to + 1);
	let nums = sel.flatMap((l) => l.new ?? []);
	const old = !nums.length;
	if (old) nums = sel.flatMap((l) => l.old ?? []);
	return { startLine: nums[0], endLine: nums.at(-1)!, old, code: sel.map((l) => l.text).join('\n') };
}

// Whether a comment ends at this line and the line still reads as it did when commented.
export const endsAt = (c: Comment, l: Line) =>
	(c.old ? l.new === undefined && l.old : l.new) === c.endLine && c.code.slice(c.code.lastIndexOf('\n') + 1) === l.text;

export const where = (c: Comment) =>
	`${c.path}:${c.startLine}${c.endLine !== c.startLine ? `–${c.endLine}` : ''}${c.old ? ' (base)' : ''}`;

export const tone = (l: string) =>
	l.startsWith('@@')
		? 'bg-blue-500/10 text-blue-300'
		: l[0] === '+'
			? 'bg-green-500/15 text-green-300'
			: l[0] === '-'
				? 'bg-red-500/15 text-red-300'
				: 'text-muted-foreground';
