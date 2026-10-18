<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import type { Avatar } from '$lib/avatar';
	import { m } from '$lib/paraglide/messages';

	interface Props {
		avatar?: Avatar;
		name?: string;
		/** The viewer is the one who called Basta. */
		you?: boolean;
		timeUp?: boolean;
		/** 3, 2, 1 while the countdown runs. */
		seconds: number;
		locked: boolean;
	}
	let { avatar, name = '', you = false, timeUp = false, seconds, locked }: Props = $props();
</script>

<div
	class="flex items-center gap-3 rounded-2xl bg-strawberry px-3 py-2 text-night shadow-pop-sm"
	data-testid="grace-banner"
>
	{#if timeUp}
		<span class="text-4xl" aria-hidden="true">⏰</span>
	{:else if avatar}
		<span class="rounded-full ring-2 ring-night"><AvatarBadge {avatar} size={44} /></span>
	{/if}
	<div class="min-w-0 flex-1">
		<p class="text-lg leading-tight font-black break-words">
			{#if timeUp}
				{m.play_time_up()}
			{:else if you}
				{m.play_you_called()}
			{:else}
				{m.play_caller_called({ name })}
			{/if}
		</p>
		{#if locked}
			<p class="text-sm leading-tight font-bold">{m.play_locked()}</p>
		{/if}
	</div>
	<!-- role=timer is aria-live=off: the countdown is never read out every second. -->
	<div
		role="timer"
		aria-label={m.play_grace_label()}
		class="grid size-14 shrink-0 place-items-center rounded-full bg-night text-4xl font-black text-banana tabular-nums"
	>
		{#if locked}<span aria-hidden="true">🔒</span><span class="sr-only">0</span
			>{:else}{seconds}{/if}
	</div>
</div>
