import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { ThreadService } from './gen/banda/v1/banda_pb';

export const api = createClient(ThreadService, createConnectTransport({ baseUrl: '/' }));
