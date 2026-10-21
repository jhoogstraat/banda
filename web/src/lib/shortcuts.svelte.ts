import type { Component } from 'svelte';
import { goto } from '$app/navigation';
import { page } from '$app/state';
import { sidebarOrder } from './threads.svelte';

// A palette action. `keys` is the shortcut hint shown next to it.
export type Command = { label: string; icon?: Component; keys?: string; run: () => unknown };

// `commands`: what the current page offers in the palette (the thread page's actions).
export const ui = $state({ palette: false, help: false, commands: [] as Command[] });

export const mod = typeof navigator !== 'undefined' && /Mac|iP/.test(navigator.platform) ? '⌘' : 'Ctrl+';

// The cheat sheet; also the hints in the palette.
export const shortcuts: [string, string][] = [
	[`${mod}K`, 'Command palette'],
	['C', 'New thread'],
	['J / K', 'Next / previous thread'],
	['Alt+↑ / ↓', 'Next / previous thread'],
	['G S', 'Settings'],
	['/', 'Focus the message box'],
	[`${mod}B`, 'Toggle the sidebar'],
	['Esc', 'Leave the message box, close dialogs'],
	['?', 'This cheat sheet']
];

const recentKey = 'banda.recent';
// Thread ids, most recently opened first.
export const recent = (): string[] => JSON.parse(localStorage.getItem(recentKey) ?? '[]');
export const visited = (id: string) =>
	localStorage.setItem(recentKey, JSON.stringify([id, ...recent().filter((r) => r !== id)].slice(0, 30)));

export function newThread() {
	goto('/').then(focusComposer);
}

export function focusComposer() {
	document.querySelector<HTMLTextAreaElement>('textarea[name=message]')?.focus();
}

function step(by: number) {
	const list = sidebarOrder();
	if (!list.length) return;
	const i = list.findIndex((t) => t.id === page.params.id);
	goto(`/t/${list[i < 0 ? 0 : (i + by + list.length) % list.length].id}`);
}

let g = 0; // when "g" was pressed, for "g s"

// The window keydown handler. Single keys never fire while typing (inputs, the prompt box, the terminal's textarea).
export function onkeydown(e: KeyboardEvent) {
	if (e.defaultPrevented || e.isComposing) return;
	const el = e.target as HTMLElement;
	const typing = el.matches?.('input, textarea, select, [contenteditable]');
	if (e.key.toLowerCase() === 'k' && (e.metaKey || e.ctrlKey) && !e.altKey && !el.closest?.('.xterm')) {
		e.preventDefault();
		ui.palette = !ui.palette;
		return;
	}
	if (typing) {
		if (e.key === 'Escape' && !el.closest('.xterm, [role=dialog]')) el.blur();
		return;
	}
	if (e.metaKey || e.ctrlKey || ui.palette || ui.help) return;
	if (e.altKey) {
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') step(e.key === 'ArrowDown' ? 1 : -1);
		else return;
	} else if (e.key === 's' && Date.now() - g < 1000) {
		g = 0;
		goto('/settings');
	} else if (e.key === 'g') g = Date.now();
	else if (e.key === 'c') newThread();
	else if (e.key === 'j' || e.key === 'k') step(e.key === 'j' ? 1 : -1);
	else if (e.key === '/') focusComposer();
	else if (e.key === '?') ui.help = true;
	else return;
	e.preventDefault();
}
