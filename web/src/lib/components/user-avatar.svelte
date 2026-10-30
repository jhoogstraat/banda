<script lang="ts">
	import { Avatar, AvatarFallback } from '#lib/components/ui/avatar/index.js';
	import { initials } from '#lib/utils.js';

	let { name, class: className = 'size-6' }: { name: string; class?: string } = $props();
	// A person keeps their color everywhere, so faces are told apart before names are read.
	const hue = $derived([...name].reduce((h, c) => (h * 31 + c.codePointAt(0)!) % 360, 0));
</script>

<Avatar class={className} title={name}><AvatarFallback class="text-[10px] font-medium text-foreground" style="background-color: oklch(0.6 0.12 {hue} / 0.3)">{initials(name)}</AvatarFallback></Avatar>
