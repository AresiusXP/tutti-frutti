<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import type { Avatar } from '$lib/avatar';
	import { m } from '$lib/paraglide/messages';

	interface Props {
		avatar?: Avatar;
		/** Who asked. */
		name: string;
		/** `ask`: the buttons; `waiting`: said yes, the others are still voting. */
		mode: 'ask' | 'waiting';
		seconds: number;
		yes: number;
		total: number;
		/** Offline: the buttons are off until the connection is back. */
		disabled?: boolean;
		onvote: (yes: boolean) => void;
	}
	let { avatar, name, mode, seconds, yes, total, disabled = false, onvote }: Props = $props();

	const button =
		'min-h-14 touch-manipulation rounded-2xl px-2 py-2 text-lg leading-tight font-black shadow-pop transition select-none active:translate-y-1 active:shadow-none disabled:opacity-50 disabled:shadow-none [@media(max-height:520px)]:min-h-12 [@media(max-height:520px)]:text-base';
</script>

<!-- NOT a dialog: it never traps focus or blocks typing, scrolling or the answer fields. -->
<section
	aria-labelledby="stop-vote-title"
	data-testid="stop-vote-sheet"
	class="flex animate-bounce-in flex-col gap-2 rounded-2xl bg-night-2 p-3 ring-2 ring-banana [@media(max-height:520px)]:gap-1 [@media(max-height:520px)]:p-2"
>
	<div class="flex items-center gap-3">
		{#if avatar && mode === 'ask'}
			<span class="shrink-0 rounded-full ring-2 ring-night"><AvatarBadge {avatar} size={40} /></span
			>
		{:else}
			<span class="shrink-0 text-3xl" aria-hidden="true">✋</span>
		{/if}
		<p
			id="stop-vote-title"
			class="min-w-0 flex-1 text-lg leading-tight font-black break-words [@media(max-height:520px)]:text-base"
		>
			{mode === 'ask' ? m.play_vote_title({ name }) : m.play_vote_waiting({ yes, total })}
		</p>
		<div
			role="timer"
			aria-label={m.play_vote_time_label()}
			class="grid size-12 shrink-0 place-items-center rounded-full bg-night text-2xl font-black text-banana tabular-nums"
		>
			{seconds}
		</div>
	</div>
	{#if mode === 'ask'}
		<div class="grid grid-cols-2 gap-2">
			<button
				type="button"
				data-testid="stop-vote-yes"
				{disabled}
				onclick={() => onvote(true)}
				class="{button} bg-strawberry text-night"
			>
				{m.play_vote_yes()}
			</button>
			<button
				type="button"
				data-testid="stop-vote-no"
				{disabled}
				onclick={() => onvote(false)}
				class="{button} bg-night-3 text-cream"
			>
				{m.play_vote_no()}
			</button>
		</div>
	{/if}
</section>
