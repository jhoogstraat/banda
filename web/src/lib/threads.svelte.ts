import { timestampMs } from '@bufbuild/protobuf/wkt';
import { api } from './api';
import { Status, type Thread } from './gen/banda/v1/banda_pb';

export const threads = $state({ list: [] as Thread[] });

const needsYou = (t: Thread) => t.status === Status.AWAITING_INPUT || t.status === Status.ERRORED;
const updated = (t: Thread) => (t.updatedAt ? timestampMs(t.updatedAt) : 0);

// Awaiting input / errored first, then most recently updated.
export async function loadThreads() {
	const { threads: list } = await api.listThreads({});
	threads.list = list.sort((a, b) => +needsYou(b) - +needsYou(a) || updated(b) - updated(a));
}
