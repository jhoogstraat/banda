// The agent's profiles in menu order: the label people see, the name the API uses, and who it is for.
export const profiles = [
	{ label: 'Engineer', name: 'engineer', note: 'Claude Code as it is, for software engineers.' },
	{ label: 'Specification', name: 'spec', note: 'Talks through what you want in plain language and writes it up as a spec.' },
	{ label: 'DevOps', name: 'devops', note: 'Explores and explains production with the credentials of your environment, read-only first.' },
	{ label: 'Business', name: 'business', note: 'Does business work for you (documents, data, automation) and shows the results, not code.' }
];
export const profileLabels = profiles.map((p) => p.label);
export const profileLabel = (name?: string) => profiles.find((p) => p.name === name)?.label ?? 'Engineer';
export const profileName = (label: string) => profiles.find((p) => p.label === label)?.name ?? 'engineer';
