import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { ThreadService, UserService } from './gen/banda/v1/banda_pb';

const transport = createConnectTransport({ baseUrl: '/' });
export const api = createClient(ThreadService, transport);
export const userApi = createClient(UserService, transport);
