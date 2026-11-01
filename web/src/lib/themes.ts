// The look chosen in Settings, per browser, next to the mode. id is <html data-theme>, set by mode-watcher: '' is the
// default. colors is the browser's bar, each mode's --background. The tokens are in routes/layout.css.
export const themes = [
	{ id: '', label: 'Highlighter', colors: { dark: '#161616', light: '#fdfdfd' } },
	{ id: 'logbook', label: 'Logbook', colors: { dark: '#0f151f', light: '#f8fafd' } },
	{ id: 'common-room', label: 'Common room', colors: { dark: '#23141f', light: '#fdf9fb' } }
];

export const themeOf = (id: string | undefined) => themes.find((t) => t.id === id) ?? themes[0];
