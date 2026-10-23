import tailwindcss from '@tailwindcss/vite';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

// Vite runs this in Node; @types/node isn't installed.
declare const process: { env: Record<string, string | undefined> };

// BANDA_API lets a second checkout run its own API and web server side by side.
const api = process.env.BANDA_API ?? 'http://localhost:8787';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			adapter: adapter({ fallback: 'index.html' })
		}),
		{
			// Previewed apps are on the same site: no framing Banda (FrameAncestors in internal/api/preview.go).
			name: 'banda-no-frames',
			configureServer: (server) =>
				void server.middlewares.use((_req, res, next) => {
					res.setHeader('Content-Security-Policy', "frame-ancestors 'none'");
					next();
				})
		}
	],
	// Connect paths look like /banda.v1.ThreadService/WatchThread -> the Go API, as do the OIDC login flow, the GitHub App's and GitLab's and the issuer's discovery.
	// /dex reaches the dev Dex, so LAN clients sign in through this one port.
	server: {
		proxy: {
			'/banda.v1.': api,
			'/auth': api,
			'/files': api,
			'/.well-known/': api, // workload identity discovery and keys, at PUBLIC_URL
			'/github': api,
			'/gitlab': api,
			'/dex': 'http://localhost:5556'
		}
	}
});
