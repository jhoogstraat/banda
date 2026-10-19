<script lang="ts">
	import { Avatar, AvatarFallback } from '#lib/components/ui/avatar/index.js';
	import { initials } from '#lib/utils.js';

	let { name, class: className = 'size-6' }: { name: string; class?: string } = $props();
</script>

<Avatar class={className} title={name}><AvatarFallback class="text-[10px] font-medium">{initials(name)}</AvatarFallback></Avatar>
