<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import type { RoomSession } from '$lib/game/session';
	import { m } from '$lib/paraglide/messages';
	import LetterSpinner from '$lib/play/LetterSpinner.svelte';
	import { OnceCommand } from '$lib/play/once.svelte';
	import { badgeAvatar } from '$lib/play/progress';
	import { roundLabel } from '$lib/play/round';

	let { session }: { session: RoomSession } = $props();

	const view = $derived(session.view);
	const chooser = $derived(view?.players.find((p) => p.id === view.chooserId));
	const usedLetters = $derived(view?.usedLetters ?? []);
	const waitingForNextRound = $derived(!session.isParticipant);

	// The server derives the letter from the moment it receives this command, so
	// the tap is sent on pointer-down (a click arrives ~100 ms later). A click
	// without a pointer-down (keyboard, assistive tech) goes through the same
	// one-shot guard, so it can never be sent twice.
	const stop = new OnceCommand(['stop_letter', 'stopLetter']);
	function pressStop() {
		stop.fire(session.lastError?.seq ?? 0, () => session.stopLetter());
	}
	function onPointerDown(event: PointerEvent) {
		if (event.button === 0) pressStop();
	}
	$effect(() => stop.failed(session.lastError));
	$effect(() => () => stop.dispose());
</script>

{#snippet header()}
	{#if view}
		<p class="text-center text-sm font-extrabold tracking-wide text-cream-muted uppercase">
			{roundLabel(view)}
		</p>
	{/if}
{/snippet}

{#snippet stopButton()}
	<button
		type="button"
		disabled={stop.locked}
		onpointerdown={onPointerDown}
		onclick={pressStop}
		class="h-[min(38dvh,16rem)] min-h-32 w-full touch-manipulation rounded-[2rem] bg-strawberry text-6xl font-black tracking-wide text-night shadow-pop transition select-none active:translate-y-1 active:scale-[0.98] active:shadow-none disabled:translate-y-1 disabled:opacity-70 disabled:shadow-none"
	>
		{m.letter_stop()}
	</button>
{/snippet}

<Screen label={m.play_letter_screen()} {header} footer={session.isChooser ? stopButton : undefined}>
	<section class="flex flex-1 flex-col items-center justify-center gap-5 text-center">
		{#if session.isChooser}
			<h1 class="text-3xl font-black text-banana">{m.play_letter_you_title()}</h1>
			<LetterSpinner size={168} />
			<p class="text-xl font-bold text-cream">{m.play_letter_tap_hint()}</p>
		{:else}
			<div class="relative">
				<LetterSpinner size={136} />
				{#if chooser}
					<span class="absolute -right-2 -bottom-1">
						<AvatarBadge avatar={badgeAvatar(chooser.avatar)} size={56} />
					</span>
				{/if}
			</div>
			<p role="status" class="text-2xl font-black text-cream">
				{m.play_letter_choosing({ name: chooser?.name ?? m.play_someone() })}
			</p>
			{#if waitingForNextRound}
				<p class="rounded-2xl bg-night-2 px-4 py-2 text-lg font-bold text-banana">
					{m.play_joins_next_round()}
				</p>
			{/if}
		{/if}

		{#if usedLetters.length > 0}
			<div class="flex flex-col items-center gap-2">
				<p class="text-sm font-bold text-cream-muted">{m.play_used_letters()}</p>
				<ul class="flex flex-wrap justify-center gap-1.5" aria-label={m.play_used_letters()}>
					{#each usedLetters as letter (letter)}
						<li
							class="grid size-8 place-items-center rounded-lg bg-night-3 text-base font-black text-cream"
						>
							{letter}
						</li>
					{/each}
				</ul>
			</div>
		{/if}
	</section>
</Screen>
