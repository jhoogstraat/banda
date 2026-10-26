// Free ports for the e2e APIs; `node web/e2e/ports.ts <n>` prints n of them (deploy/dev.sh).
// listen(0) on :: may hand out a port another process holds on 127.0.0.1 alone (on macOS, e.g. an editor's
// WebSocket server, which answers 426), and an API then binds it without error while localhost reaches the
// other process. So a port counts as free only where localhost resolves too.
import { createServer } from 'node:net';

const listen = (port: number, host: string) =>
	new Promise<number | undefined>((resolve) => {
		const s = createServer()
			.once('error', (e: NodeJS.ErrnoException) => resolve(e.code === 'EADDRINUSE' ? undefined : port)) // e.g. no IPv6
			.listen(port, host, () => {
				const { port } = s.address() as { port: number };
				s.close(() => resolve(port));
			});
	});

export async function freePort(): Promise<number> {
	for (;;) {
		const port = (await listen(0, '127.0.0.1'))!;
		if ((await listen(port, '::1')) && (await listen(port, '::'))) return port;
	}
}

if (import.meta.main) {
	const ports = new Set<number>();
	while (ports.size < Number(process.argv[2] ?? 1)) ports.add(await freePort());
	console.log([...ports].join(' '));
}
