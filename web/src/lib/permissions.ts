import { PermissionMode } from './gen/banda/v1/banda_pb.ts';

// The composer chip's labels for the thread's permission mode, in menu order.
export const permissionModes: Record<string, PermissionMode> = {
	'Bypass permissions': PermissionMode.BYPASS,
	'Ask before edits and commands': PermissionMode.ASK,
	'Accept edits': PermissionMode.ACCEPT_EDITS,
	Plan: PermissionMode.PLAN
};
export const permissionLabel = (m?: PermissionMode) =>
	Object.keys(permissionModes).find((k) => permissionModes[k] === m) ?? 'Bypass permissions';
