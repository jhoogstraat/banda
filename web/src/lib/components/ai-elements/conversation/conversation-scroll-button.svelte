<script lang="ts" module>
	import { cn } from "#lib/utils.js";
	import type { ButtonProps } from "#lib/components/ui/button/index.js";

	export interface ConversationScrollButtonProps extends ButtonProps {}
</script>

<script lang="ts">
	import { Button } from "#lib/components/ui/button/index.js";
	import ArrowDown from "@lucide/svelte/icons/arrow-down";
	import { getStickToBottomContext } from "./stick-to-bottom-context.svelte.js";
	import { fly } from "svelte/transition";

	let { class: className, onclick, ...restProps }: ConversationScrollButtonProps = $props();

	const context = getStickToBottomContext();

	const handleScrollToBottom = (event: MouseEvent) => {
		context.scrollToBottom();
		if (onclick) {
			onclick(
				event as MouseEvent & {
					currentTarget: EventTarget & HTMLButtonElement;
				}
			);
		}
	};
</script>

{#if !context.isAtBottom}
	<div
		transition:fly={{ duration: 150, y: 10 }}
		class="absolute bottom-4 left-[50%] translate-x-[-50%]"
	>
		<Button
			class={cn(
				"bg-background/80 border-border/50 hover:bg-background/90 rounded-full shadow-lg backdrop-blur-sm hover:shadow-xl",
				className
			)}
			onclick={handleScrollToBottom}
			aria-label="Scroll to the latest message"
			size="icon"
			type="button"
			variant="outline"
			{...restProps}
		>
			<ArrowDown class="size-4" />
		</Button>
	</div>
{/if}
