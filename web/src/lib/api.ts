import { Code, ConnectError, createClient, type Interceptor } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { fromJson } from '@bufbuild/protobuf';
import { AttachmentSchema, SettingsService, ThreadService, UserService, type Attachment } from './gen/banda/v1/banda_pb.js';

// Signed out (or session expired): Banda's sign-in page, which comes back to this page.
// ponytail: unary calls only. Streams report errors while iterating, but every page makes a unary call first.
const signIn: Interceptor = (next) => async (req) => {
	try {
		return await next(req);
	} catch (e) {
		if (ConnectError.from(e).code === Code.Unauthenticated) {
			location.href = `/signin?next=${encodeURIComponent(location.pathname + location.search)}`;
		}
		throw e;
	}
};

const transport = createConnectTransport({ baseUrl: '/', interceptors: [signIn] });
export const api = createClient(ThreadService, transport);
export const userApi = createClient(UserService, transport);
export const settingsApi = createClient(SettingsService, transport);

// Uploads a file to attach to a message (see FilesHandler in internal/api).
export async function upload(file: File): Promise<Attachment> {
	const res = await fetch(`/files?name=${encodeURIComponent(file.name)}`, {
		method: 'POST',
		headers: { 'Content-Type': file.type || 'application/octet-stream' },
		body: file
	});
	if (!res.ok) throw new Error(`${file.name}: ${(await res.text()).trim()}`);
	return fromJson(AttachmentSchema, await res.json());
}
