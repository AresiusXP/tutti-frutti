<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import Screen from '$lib/components/Screen.svelte';
	import type { RoomSession } from '$lib/game/session';
	import { m } from '$lib/paraglide/messages';
	import AnswerField from '$lib/play/AnswerField.svelte';
	import BastaSplash from '$lib/play/BastaSplash.svelte';
	import GraceBanner from '$lib/play/GraceBanner.svelte';
	import PlayHeader from '$lib/play/PlayHeader.svelte';
	import StopConfirmSheet from '$lib/play/StopConfirmSheet.svelte';
	import StopVotePrompt from '$lib/play/StopVotePrompt.svelte';
	import StopWaitBanner from '$lib/play/StopWaitBanner.svelte';
	import { fieldStatus } from '$lib/play/answer';
	import { AnswerDrafts, DEBOUNCE_MS, GRACE_DEBOUNCE_MS } from '$lib/play/drafts.svelte';
	import { OnceCommand } from '$lib/play/once.svelte';
	import { badgeAvatar, playerProgress } from '$lib/play/progress';
	import { roundLabel } from '$lib/play/round';
	import { STOP_VOTE_SECONDS, stopVoteInfo } from '$lib/play/stopvote';
	import {
		GRACE_MS,
		SPLASH_MS,
		WARN_SECONDS,
		formatClock,
		parseDeadline,
		secondsLeft,
		splashStillRelevant,
		timerTone
	} from '$lib/play/time';
	import { createWakeLock } from '$lib/play/wakelock';
	import { toasts } from '$lib/toast.svelte';

	let { session }: { session: RoomSession } = $props();

	const view = $derived(session.view);
	const round = $derived(view?.round);
	const phase = $derived(view?.phase);
	const letter = $derived(round?.letter ?? '');
	const lang = $derived(view?.settings.lang ?? 'es');
	const labels = $derived(view?.categoryLabels ?? []);
	const total = $derived(view?.settings.categories.length ?? 0);
	const participant = $derived(session.isParticipant);
	const ownAnswers = $derived(round && view ? (round.answers[view.youId] ?? []) : []);

	// Server clock, refreshed ~4x/s (countdowns, lock).
	let now = $state(untrack(() => session.serverNow()));
	onMount(() => {
		const timer = setInterval(() => (now = session.serverNow()), 250);
		return () => clearInterval(timer);
	});

	// --- drafts ---------------------------------------------------------------
	const drafts = new AnswerDrafts(
		untrack(() => total),
		(category, text) => session.setAnswer(category, text),
		untrack(() => ownAnswers)
	);
	let seededRound = untrack(() => round?.number);
	$effect(() => {
		const number = round?.number;
		const size = total;
		const own = [...ownAnswers];
		untrack(() => {
			if (number !== seededRound || size !== drafts.size) {
				seededRound = number;
				drafts.reset(size, own);
			} else {
				drafts.adopt(own);
			}
		});
	});
	$effect(() => () => drafts.dispose());

	let prevStatus = untrack(() => session.status);
	$effect(() => {
		const status = session.status;
		const own = ownAnswers;
		untrack(() => {
			if (status === 'connected' && prevStatus !== 'connected') drafts.resync(own);
			prevStatus = status;
		});
	});

	onMount(() => {
		const onHidden = () => {
			if (document.visibilityState === 'hidden') drafts.flushAll();
		};
		document.addEventListener('visibilitychange', onHidden);
		return () => document.removeEventListener('visibilitychange', onHidden);
	});

	// --- validation -----------------------------------------------------------
	const statuses = $derived(
		labels.map((_, i) => fieldStatus(drafts.values[i] ?? '', letter, lang))
	);
	const filledCount = $derived(statuses.filter((s) => s !== 'empty').length);
	const wrongLabels = $derived(labels.filter((_, i) => statuses[i] === 'wrong_letter'));
	const emptyCount = $derived(statuses.filter((s) => s === 'empty').length);
	const wrongCount = $derived(statuses.filter((s) => s === 'wrong_letter').length);
	const allValid = $derived(total > 0 && statuses.every((s) => s === 'valid'));

	// --- time limit -----------------------------------------------------------
	const limitEnds = $derived(parseDeadline(round?.timeLimitEndsAt));
	const limitSeconds = $derived(limitEnds === null ? null : secondsLeft(limitEnds, now));
	const clock = $derived(
		phase === 'playing' && limitSeconds !== null ? formatClock(limitSeconds) : null
	);
	const tone = $derived(timerTone(limitSeconds ?? Infinity));
	const timeWarning = $derived(
		phase === 'playing' && limitSeconds !== null && limitSeconds > 0 && limitSeconds <= WARN_SECONDS
	);

	// --- grace ----------------------------------------------------------------
	const callerId = $derived(round?.callerId ?? '');
	const caller = $derived(view?.players.find((p) => p.id === callerId));
	const timeUp = $derived(phase === 'grace' && callerId === '');
	const graceEnds = $derived(parseDeadline(round?.graceEndsAt));
	const graceSeconds = $derived(
		graceEnds === null ? 0 : Math.min(GRACE_MS / 1000, secondsLeft(graceEnds, now))
	);
	const locked = $derived(phase === 'grace' && graceEnds !== null && now >= graceEnds);

	$effect(() => {
		if (phase !== 'grace') {
			drafts.debounceMs = DEBOUNCE_MS;
			return;
		}
		drafts.debounceMs = GRACE_DEBOUNCE_MS;
		// The room just went to grace: whatever is still pending must reach the server now.
		untrack(() => drafts.flushAll());
	});
	$effect(() => {
		if (locked) untrack(() => drafts.flushAll());
	});

	// --- announcements, splash, reveal ----------------------------------------
	// What the assertive live region says. Kept as data (not text) so a language switch re-renders it.
	let announced = $state<'' | 'letter' | 'basta' | 'time_up' | 'stop_vote'>('');
	let announcedCaller = $state('');
	const announcement = $derived(
		announced === 'letter'
			? m.play_letter_announce({ letter })
			: announced === 'basta'
				? `${announcedCaller}: ${m.basta_overlay()}`
				: announced === 'time_up'
					? m.play_time_up()
					: announced === 'stop_vote'
						? m.play_vote_title({ name: announcedCaller })
						: ''
	);
	let splash = $state(false);
	let splashTimer: ReturnType<typeof setTimeout> | undefined;

	function vibrate(pattern: number[]) {
		try {
			navigator.vibrate?.(pattern);
		} catch {
			// vibration is optional (permissions, desktop browsers)
		}
	}

	function startSplash() {
		announcedCaller = caller?.name ?? m.play_someone();
		announced = timeUp ? 'time_up' : 'basta';
		splash = true;
		vibrate([200, 100, 200]);
		clearTimeout(splashTimer);
		splashTimer = setTimeout(() => (splash = false), SPLASH_MS);
	}

	let prevPhase: string | undefined;
	$effect(() => {
		const current = phase;
		untrack(() => {
			if (current === 'grace' && prevPhase !== 'grace') {
				const firstRender = prevPhase === undefined;
				if (!firstRender || splashStillRelevant(graceEnds, session.serverNow())) startSplash();
			}
			prevPhase = current;
		});
	});

	/** The letter was revealed moments ago (not a reload): play the big reveal. */
	function freshReveal(): boolean {
		if (phase !== 'playing' || !round) return false;
		if (globalThis.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return false;
		const recent = (at: number) =>
			Math.abs(Date.now() - at) < 5000 || Math.abs(session.serverNow() - at) < 5000;
		return session.events.some(
			(e) =>
				e.type === 'letter_revealed' &&
				(e.round === undefined || e.round === round.number) &&
				recent(e.at)
		);
	}
	let reveal = $state(untrack(freshReveal));
	let revealTimer: ReturnType<typeof setTimeout> | undefined;

	onMount(() => {
		// Set after mount so the live region is announced (content present at insertion is not).
		if (announced === '') announced = 'letter';
		if (reveal) revealTimer = setTimeout(() => (reveal = false), 1200);
		// With a mouse/keyboard there is no on-screen keyboard to pop up: start typing right away.
		if (participant && globalThis.matchMedia?.('(pointer: fine)').matches) {
			inputs[0]?.focus({ preventScroll: true });
		}
		const wakeLock = createWakeLock();
		wakeLock.acquire();
		return () => {
			wakeLock.release();
			clearTimeout(splashTimer);
			clearTimeout(revealTimer);
		};
	});

	// --- basta and the vote to stop -------------------------------------------
	const basta = new OnceCommand(['basta'], 4000);
	const ask = new OnceCommand(['ask_stop'], 4000);
	const vote = new OnceCommand(['vote_stop'], 4000);
	const online = $derived(session.status === 'connected');
	const stopVoteOn = $derived(view?.settings.stopVote === true);
	const info = $derived(stopVoteInfo(view, participant));
	const voteSeconds = $derived(
		info?.endsAt != null ? Math.min(STOP_VOTE_SECONDS, secondsLeft(info.endsAt, now)) : 0
	);
	const playingNow = $derived(participant && phase === 'playing');
	// The asker may call Basta as soon as everything is filled in: it ends their own vote.
	const canBasta = $derived(playingNow && allValid && online && !basta.locked);
	/** Answers missing: tapping the button asks everyone to stop (after a confirmation). */
	const canAsk = $derived(
		playingNow && stopVoteOn && online && !info && !allValid && total > 0 && !ask.locked
	);
	const asking = $derived((info?.role === 'asker' || (ask.locked && !info)) && !allValid);
	const hint = $derived(
		!online
			? m.play_hint_offline()
			: asking
				? m.play_hint_ask_wait()
				: canAsk
					? wrongLabels.length > 0
						? m.play_hint_ask_wrong({ categories: wrongLabels.join(', ') })
						: emptyCount === 1
							? m.play_hint_ask_one()
							: m.play_hint_ask_many({ count: emptyCount })
					: wrongLabels.length > 0
						? m.play_hint_wrong({ categories: wrongLabels.join(', ') })
						: emptyCount > 0
							? m.play_hint_fill()
							: m.play_hint_ready()
	);
	$effect(() => basta.failed(session.lastError));
	$effect(() => ask.failed(session.lastError));
	$effect(() => vote.failed(session.lastError));
	$effect(() => () => {
		basta.dispose();
		ask.dispose();
		vote.dispose();
	});

	function callBasta() {
		if (!canBasta) return;
		basta.fire(session.lastError?.seq ?? 0, () => {
			drafts.flushAll();
			session.basta();
		});
	}

	let confirmOpen = $state(false);
	let mainButton = $state<HTMLButtonElement>();

	function onMain() {
		if (canBasta) callBasta();
		else if (canAsk) confirmOpen = true;
	}

	/** The confirmation is stale as soon as asking is no longer possible (someone else's vote, a Basta, offline...). */
	$effect(() => {
		if (confirmOpen && !canAsk) confirmOpen = false;
	});

	function keepPlaying() {
		confirmOpen = false;
		void tick().then(() => mainButton?.focus());
	}

	function confirmAsk() {
		confirmOpen = false;
		if (!canAsk) return;
		ask.fire(session.lastError?.seq ?? 0, () => {
			drafts.flushAll();
			session.askStop();
		});
	}

	// The vote itself is state (`round.stopVote`); the tap only remembers what this viewer answered
	// until the next snapshot says so.
	let choice = $state<'yes' | 'no' | null>(null);
	let openVote = '';
	let wasOpen = false;
	let wasOnline = untrack(() => online);
	$effect(() => {
		const now = online;
		untrack(() => {
			// A command sent just before the socket dropped may be lost, and a reconnecting voter has
			// not voted yet: the next snapshot says where things stand.
			if (now && !wasOnline) {
				vote.unlock();
				ask.unlock();
				choice = null;
			}
			wasOnline = now;
		});
	});
	$effect(() => {
		const key = info?.key ?? '';
		untrack(() => {
			if (key !== openVote) {
				openVote = key;
				vote.unlock();
				choice = null;
			}
			// Asking again must be possible right after a vote ended (there is no cooldown).
			if (wasOpen && !info) ask.unlock();
			wasOpen = !!info;
		});
	});

	const prompt = $derived.by<'ask' | 'waiting' | null>(() => {
		if (info?.role === 'voted') return 'waiting';
		if (info?.role !== 'voter') return null;
		if (!vote.locked) return 'ask';
		return choice === 'yes' ? 'waiting' : null;
	});
	const promptYes = $derived(
		(info?.yes ?? 0) + (info?.role === 'voter' && choice === 'yes' ? 1 : 0)
	);
	const asker = $derived(view?.players.find((p) => p.id === info?.callerId));

	function castVote(yes: boolean) {
		if (info?.role !== 'voter' || !online) return;
		vote.fire(session.lastError?.seq ?? 0, () => {
			choice = yes ? 'yes' : 'no';
			if (yes) drafts.flushAll();
			session.voteStop(yes);
		});
	}

	// A new vote for this viewer: said aloud and felt, like the Basta call.
	let announcedVote = '';
	$effect(() => {
		const current = info;
		untrack(() => {
			if (!current) {
				announcedVote = '';
				if (announced === 'stop_vote') announced = '';
				return;
			}
			if (current.role !== 'voter' || current.key === announcedVote) return;
			announcedVote = current.key;
			announcedCaller = asker?.name ?? m.play_someone();
			announced = 'stop_vote';
			if (!globalThis.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
				vibrate([120, 80, 120]);
			}
		});
	});

	// Only the asker hears about a failed vote (and why); the others just see the prompt go away.
	let seenEvent = untrack(() => session.events.at(-1)?.seq ?? 0);
	$effect(() => {
		const events = session.events;
		untrack(() => {
			for (const event of events) {
				if (event.seq <= seenEvent) continue;
				seenEvent = event.seq;
				if (event.type !== 'stop_rejected' || event.playerId !== view?.youId) continue;
				ask.unlock();
				if (event.reason === 'declined') toasts.show(m.play_stop_declined());
				else if (event.reason === 'timeout') toasts.show(m.play_stop_timeout());
			}
		});
	});

	// The footer grows with the banner or the prompt: keep the field being typed in clear of it.
	let scrollTimer: ReturnType<typeof setTimeout> | undefined;
	$effect(() => {
		const marker = info ? `${info.key}|${info.role}|${prompt}` : '';
		if (!marker) return;
		untrack(() => {
			const active = document.activeElement;
			if (!(active instanceof HTMLInputElement) || !inputs.includes(active)) return;
			clearTimeout(scrollTimer);
			scrollTimer = setTimeout(() => active.scrollIntoView?.({ block: 'nearest' }), 80);
		});
	});
	$effect(() => () => clearTimeout(scrollTimer));

	// --- fields ---------------------------------------------------------------
	let inputs = $state<(HTMLInputElement | null | undefined)[]>([]);
	function focusNext(index: number) {
		const next = inputs[index + 1];
		if (next && !next.disabled) next.focus();
		else inputs[index]?.blur();
	}

	const entries = $derived(view ? playerProgress(view, filledCount) : []);
	const callerAvatar = $derived(caller ? badgeAvatar(caller.avatar) : undefined);
</script>

{#snippet banner()}
	<GraceBanner
		avatar={callerAvatar}
		name={caller?.name ?? m.play_someone()}
		you={callerId !== '' && callerId === view?.youId}
		{timeUp}
		seconds={graceSeconds}
		{locked}
	/>
{/snippet}

{#snippet onlooker()}
	<p
		data-testid="stop-vote-onlooker"
		class="rounded-2xl bg-night-2 px-4 py-2 text-base font-bold text-banana ring-1 ring-night-3"
	>
		{m.play_vote_onlooker({ name: asker?.name ?? m.play_someone() })}
	</p>
{/snippet}

{#snippet footerStack()}
	{#if info?.role === 'asker'}
		<StopWaitBanner seconds={voteSeconds} yes={info.yes} total={info.total} />
	{:else if prompt && info}
		<StopVotePrompt
			avatar={asker ? badgeAvatar(asker.avatar) : undefined}
			name={asker?.name ?? m.play_someone()}
			mode={prompt}
			seconds={voteSeconds}
			yes={promptYes}
			total={info.total}
			disabled={!online}
			onvote={castVote}
		/>
	{:else if info?.role === 'onlooker'}
		{@render onlooker()}
	{/if}
	<p id="basta-hint" class="min-h-5 text-center text-sm font-bold text-cream-muted">{hint}</p>
	<button
		bind:this={mainButton}
		type="button"
		data-testid="basta-button"
		disabled={!canBasta && !canAsk}
		class:basta-ready={canBasta}
		aria-describedby="basta-hint"
		aria-haspopup={canAsk ? 'dialog' : undefined}
		onclick={onMain}
		class="h-[4.5rem] w-full touch-manipulation rounded-[1.75rem] text-4xl font-black tracking-wide shadow-pop transition select-none active:translate-y-1 active:shadow-none disabled:bg-night-3 disabled:text-cream-muted disabled:shadow-none [@media(max-height:520px)]:h-14 [@media(max-height:520px)]:text-2xl {canBasta
			? 'bg-strawberry text-night'
			: canAsk
				? 'bg-night-3 text-cream ring-2 ring-strawberry'
				: ''}"
	>
		{m.basta_button()}
	</button>
{/snippet}

{#if view && round}
	<Screen
		label={m.play_screen_label({ n: round.number, letter })}
		footer={participant && phase === 'playing' ? footerStack : undefined}
	>
		<h1 class="sr-only">{m.play_screen_label({ n: round.number, letter })}</h1>
		<PlayHeader
			{letter}
			round={roundLabel(view)}
			{clock}
			{tone}
			{entries}
			pop={reveal}
			banner={phase === 'grace' ? banner : undefined}
		/>

		{#if participant}
			<div role="group" aria-label={m.play_answers_label()} class="flex flex-col gap-3 pb-2">
				{#each labels as label, i (i)}
					<AnswerField
						id="answer-{i}"
						{label}
						value={drafts.values[i] ?? ''}
						status={statuses[i] ?? 'empty'}
						{letter}
						disabled={locked}
						last={i === labels.length - 1}
						bind:input={inputs[i]}
						oninput={(text) => drafts.input(i, text)}
						onblur={() => drafts.flush(i)}
						onenter={() => focusNext(i)}
					/>
				{/each}
			</div>
		{:else}
			<p
				class="flex items-center gap-3 rounded-2xl bg-night-2 px-4 py-4 text-lg font-bold text-banana"
			>
				<span class="text-4xl" aria-hidden="true">👀</span>
				<span>{m.play_spectator()}</span>
			</p>
			{#if info}{@render onlooker()}{/if}
		{/if}
	</Screen>

	<p role="alert" aria-live="assertive" aria-atomic="true" class="sr-only">{announcement}</p>
	<p aria-live="polite" class="sr-only">{timeWarning ? m.play_time_warning() : ''}</p>

	{#if reveal}
		<div
			class="pointer-events-none fixed inset-0 z-40 grid place-items-center"
			aria-hidden="true"
			data-testid="letter-reveal"
		>
			<span class="reveal text-[min(60vmin,16rem)] leading-none font-black text-banana">
				{letter}
			</span>
		</div>
	{/if}

	{#if confirmOpen}
		<StopConfirmSheet
			{letter}
			empty={emptyCount}
			wrong={wrongCount}
			seconds={STOP_VOTE_SECONDS}
			onconfirm={confirmAsk}
			oncancel={keepPlaying}
		/>
	{/if}

	{#if splash}
		<BastaSplash
			avatar={timeUp ? undefined : callerAvatar}
			name={caller?.name ?? ''}
			{timeUp}
			onclose={() => (splash = false)}
		/>
	{/if}
{/if}

<style>
	.reveal {
		text-shadow:
			0 0.04em 0 rgb(0 0 0 / 0.35),
			0 0 0.4em rgb(255 210 63 / 0.5);
		animation: reveal 1.2s cubic-bezier(0.34, 1.56, 0.64, 1) both;
	}
	@keyframes reveal {
		0% {
			opacity: 0;
			transform: scale(0.2) rotate(-14deg);
		}
		30% {
			opacity: 1;
			transform: scale(1.15) rotate(3deg);
		}
		45% {
			transform: scale(1) rotate(0);
		}
		75% {
			opacity: 1;
			transform: scale(1);
		}
		100% {
			opacity: 0;
			transform: translateY(-38vh) scale(0.3);
		}
	}
	/* A glow, not a transform: the button must not move, or automated clicks wait for it to settle. */
	.basta-ready {
		animation: basta-pulse 1.6s ease-out infinite;
	}
	@keyframes basta-pulse {
		from {
			box-shadow:
				0 5px 0 0 rgb(0 0 0 / 0.35),
				0 0 0 0 rgb(255 79 129 / 0.6);
		}
		to {
			box-shadow:
				0 5px 0 0 rgb(0 0 0 / 0.35),
				0 0 0 18px rgb(255 79 129 / 0);
		}
	}
</style>
