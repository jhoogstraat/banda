import { Code, ConnectError, createClient, type Interceptor } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { ThreadService, UserService } from './gen/banda/v1/banda_pb';

// Signed out (or session expired): sign in again and come back to this page.
// ponytail: unary calls only. Streams report errors while iterating, but every page makes a unary call first.
const signIn: Interceptor = (next) => async (req) => {
	try {
		return await next(req);
	} catch (e) {
		if (ConnectError.from(e).code === Code.Unauthenticated) {
			location.href = `/auth/login?next=${encodeURIComponent(location.pathname + location.search)}`;
		}
		throw e;
	}
};

const transport = createConnectTransport({ baseUrl: '/', interceptors: [signIn] });
export const api = createClient(ThreadService, transport);
export const userApi = createClient(UserService, transport);
