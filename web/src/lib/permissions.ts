import { PermissionMode } from './gen/banda/v1/banda_pb.js';

// The composer chip's labels for the thread's permission mode, in menu order.
export const permissionModes: Record<string, PermissionMode> = {
	'Bypass permissions': PermissionMode.BYPASS,
	'Ask before edits and commands': PermissionMode.ASK,
	'Accept edits': PermissionMode.ACCEPT_EDITS,
	Plan: PermissionMode.PLAN
};
export const permissionLabel = (m?: PermissionMode) =>
	Object.keys(permissionModes).find((k) => permissionModes[k] === m) ?? 'Bypass permissions';

// Narrower "always allow" rules than the whole tool, in Claude Code's syntax (the runner matches them):
// a command and its subcommand for Bash, the file's directory for edits. '' when none makes sense.
export function narrowRule(tool: string, args: Record<string, any>): string {
	if (tool === 'Bash') {
		const command = String(args.command ?? '').trim();
		if (!command || /[;&|<>$`()\n\\]/.test(command)) return '';
		const [a, b] = command.split(/\s+/);
		return `Bash(${b && /^[a-z][\w:.-]*$/.test(b) ? `${a} ${b}` : a}:*)`;
	}
	if (['Edit', 'Write', 'MultiEdit', 'NotebookEdit'].includes(tool)) {
		const path = String(args.file_path ?? args.notebook_path ?? '').replace(/^\/home\/banda\/work\//, '');
		const dir = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
		return dir && !dir.startsWith('/') && !dir.split('/').includes('..') ? `Edit(${dir}/**)` : '';
	}
	return '';
}

// How a rule reads in a button or the Allowed list: "npm test …", "edits in src/".
export const ruleLabel = (rule: string) =>
	rule.replace(/^Bash\((.*):\*\)$/, '$1 …').replace(/^Edit\((.*)\/\*\*\)$/, 'edits in $1/');
