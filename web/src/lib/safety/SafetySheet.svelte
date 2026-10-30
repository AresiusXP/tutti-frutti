<script lang="ts">
	import { tick, untrack } from 'svelte';
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import Button from '$lib/components/Button.svelte';
	import { errorMessage } from '$lib/errors';
	import type { RoomSession } from '$lib/game/session';
	import Sheet from '$lib/lobby/Sheet.svelte';
	import { historySettled } from '$lib/lobby/historyPop';
	import { m } from '$lib/paraglide/messages';
	import {
		ReportKindAIVerdict,
		ReportKindAnswer,
		ReportKindCustomLabel,
		ReportKindPlayer
	} from '$lib/protocol/api';
	import { toasts } from '$lib/toast.svelte';
	import type { SafetyApi } from './api';
	import { blocks as sharedBlocks, type BlocksStore } from './blocks.svelte';
	import type { SafetyRequest } from './context';
	import { ReportDraft } from './report.svelte';
	import {
		canTarget,
		MAX_COMMENT,
		REPORT_REASONS,
		type Person,
		type ReportTarget,
		type Shown
	} from './targets';

	/**
	 * The one sheet of the safety layer, in steps so that it owns a single history entry (Android Back
	 * closes it in one press): the other players -> the menu of one person -> the report form or the
	 * "Block and leave" confirmation. Reporting and blocking need no terms acceptance.
	 */
	let {
		session,
		request,
		onclose,
		api,
		blocks = sharedBlocks
	}: {
		session: RoomSession;
		request: SafetyRequest;
		onclose: () => void;
		/** Replaced in tests. */
		api?: SafetyApi;
		blocks?: BlocksStore;
	} = $props();

	type Step =
		| { name: 'players' }
		| { name: 'menu'; person: Person; answer?: Shown; back: Step | null }
		| { name: 'report'; draft: ReportDraft; back: Step | null }
		| { name: 'block'; person: Person; back: Step };

	const context = () => ({ code: session.code, roomInstanceId: session.roomInstanceId });

	function reportStep(target: ReportTarget, back: Step | null): Step {
		return { name: 'report', draft: new ReportDraft(target, context, { api }), back };
	}

	function first(): Step {
		switch (request.start) {
			case 'players':
				return { name: 'players' };
			case 'person':
				return { name: 'menu', person: request.person, answer: request.answer, back: null };
			case 'verdict':
				return reportStep(
					{ kind: ReportKindAIVerdict, person: request.person, ...request.verdict },
					null
				);
			case 'label':
				return reportStep(
					{ kind: ReportKindCustomLabel, category: request.category, label: request.label },
					null
				);
		}
	}

	// The request never changes while the sheet is open (the host remounts it for another one).
	let step = $state.raw<Step>(untrack(first));
	let body = $state<HTMLElement>();

	const youId = $derived(session.view?.youId ?? '');
	const others = $derived(session.players.filter((p) => canTarget(youId, p.id)));

	const title = $derived(
		step.name === 'players'
			? m.report_players_title()
			: step.name === 'menu'
				? m.report_menu_title({ name: step.person.name })
				: step.name === 'block'
					? m.block_title()
					: m.report_title()
	);

	/** Moves to another step and puts the focus on its first control (the sheet stays one dialog). */
	async function go(next: Step) {
		step = next;
		await tick();
		// The whole dialog: the Cancel button of a confirmation lives in the sheet's footer.
		const root = body?.closest<HTMLElement>('[role="dialog"]');
		(
			root?.querySelector<HTMLElement>('[data-autofocus]') ??
			body?.querySelector<HTMLElement>('button, input, textarea')
		)?.focus();
	}

	function back() {
		if (step.name !== 'players' && step.back) void go(step.back);
		else onclose();
	}

	// ---- report -------------------------------------------------------------------------------

	const draft = $derived(step.name === 'report' ? step.draft : null);

	function aboutText(target: ReportTarget): string {
		switch (target.kind) {
			case ReportKindPlayer:
				return m.report_about_player({ name: target.person.name });
			case ReportKindCustomLabel:
				return m.report_about_label({ label: target.label });
			case ReportKindAnswer:
				return m.report_about_answer({ name: target.person.name, text: target.text });
			case ReportKindAIVerdict:
				return m.report_about_verdict({ name: target.person.name });
		}
	}

	const reasonText: Record<(typeof REPORT_REASONS)[number], () => string> = {
		harassment: () => m.report_reason_harassment(),
		hate: () => m.report_reason_hate(),
		sexual: () => m.report_reason_sexual(),
		violence: () => m.report_reason_violence(),
		self_harm: () => m.report_reason_self_harm(),
		spam: () => m.report_reason_spam(),
		impersonation: () => m.report_reason_impersonation(),
		other: () => m.report_reason_other()
	};

	async function send() {
		if (!draft) return;
		if (await draft.send()) {
			toasts.show(m.report_sent());
			onclose();
		}
	}

	// ---- block --------------------------------------------------------------------------------

	let blocking = $state(false);
	let blockError = $state<string | null>(null);

	async function confirmBlock(person: Person) {
		if (blocking) return;
		blocking = true;
		blockError = null;
		blocks.expectRemoval(session.code);
		const result = await blocks.block(person);
		blocking = false;
		if (!result.ok) {
			blocks.expectRemoval(null);
			blockError = result.code;
			return;
		}
		toasts.show(m.block_done({ name: person.name }));
		onclose();
		// Leave only once the sheet's history entry is gone: the room route sends the player Home next, and a pop
		// that arrives after that navigation started would cancel it.
		await tick();
		await historySettled();
		if (session.status !== 'closed') {
			// The server already took this seat out of every room the two shared: no second `leave`.
			if (result.leftRooms.includes(session.code)) session.leaveLocally();
			else session.leave();
		}
	}

	const row =
		'flex min-h-14 w-full items-center gap-3 rounded-xl bg-night px-3 text-left text-lg font-bold ring-1 ring-night-3 transition active:bg-night-3';
</script>

<Sheet {title} {onclose} initialFocus="[data-autofocus]">
	<div bind:this={body} class="flex flex-col gap-3 pb-2" data-testid="safety-sheet">
		{#if step.name === 'players'}
			{#if others.length === 0}
				<p class="text-lg text-cream-muted">{m.report_players_none()}</p>
			{:else}
				<p class="text-cream-muted">{m.report_players_hint()}</p>
				<ul class="flex flex-col gap-2">
					{#each others as player, i (player.id)}
						<li>
							<button
								type="button"
								class={row}
								data-autofocus={i === 0 ? '' : undefined}
								onclick={() =>
									go({ name: 'menu', person: { id: player.id, name: player.name }, back: step })}
							>
								<AvatarBadge avatar={player.avatar} size={36} />
								<span class="min-w-0 flex-1 truncate">{player.name}</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		{:else if step.name === 'menu'}
			{@const menu = step}
			<ul class="flex flex-col gap-2">
				{#if menu.answer}
					{@const answer = menu.answer}
					<li>
						<button
							type="button"
							class={row}
							data-autofocus
							onclick={() =>
								go(reportStep({ kind: ReportKindAnswer, person: menu.person, ...answer }, menu))}
						>
							{m.report_menu_answer()}
						</button>
					</li>
				{/if}
				<li>
					<button
						type="button"
						class={row}
						data-autofocus={menu.answer ? undefined : ''}
						onclick={() => go(reportStep({ kind: ReportKindPlayer, person: menu.person }, menu))}
					>
						{m.report_menu_player()}
					</button>
				</li>
				<li>
					<button
						type="button"
						class="{row} text-strawberry"
						onclick={() => go({ name: 'block', person: menu.person, back: menu })}
					>
						{m.report_menu_block()}
					</button>
				</li>
			</ul>
		{:else if step.name === 'report'}
			{@const d = step.draft}
			<p
				class="rounded-xl bg-night px-3 py-2 text-base [overflow-wrap:anywhere] ring-1 ring-night-3"
			>
				{aboutText(d.target)}
			</p>

			<fieldset
				class="flex flex-col gap-1.5"
				aria-describedby={d.missingReason ? 'report-reason-error' : undefined}
			>
				<legend class="pb-1 text-lg font-black">{m.report_reason_legend()}</legend>
				{#each REPORT_REASONS as reason, i (reason)}
					<label
						class="flex min-h-12 cursor-pointer items-center gap-3 rounded-xl bg-night px-3 py-1 text-base font-bold ring-1 ring-night-3 has-checked:bg-night-3 has-checked:ring-banana"
					>
						<input
							type="radio"
							id="report-reason-{reason}"
							name="report-reason"
							value={reason}
							autocomplete="off"
							bind:group={d.reason}
							data-autofocus={i === 0 ? '' : undefined}
							class="size-5 shrink-0 accent-banana"
						/>
						{reasonText[reason]()}
					</label>
				{/each}
				{#if d.missingReason && !d.reason}
					<p id="report-reason-error" role="alert" class="text-sm font-bold text-strawberry">
						{m.report_reason_required()}
					</p>
				{/if}
			</fieldset>

			<div class="flex flex-col gap-1">
				<div class="flex items-baseline justify-between gap-2">
					<label for="report-comment" class="text-base font-bold">{m.report_comment_label()}</label>
					<span class="text-sm text-cream-muted tabular-nums" aria-hidden="true">
						{MAX_COMMENT - d.remaining}/{MAX_COMMENT}
					</span>
				</div>
				<textarea
					id="report-comment"
					name="comment"
					rows="3"
					maxlength={MAX_COMMENT}
					autocomplete="off"
					spellcheck="false"
					bind:value={d.comment}
					aria-describedby="report-comment-hint"
					class="w-full resize-none rounded-xl bg-night px-3 py-2 text-base ring-2 ring-night-3 focus:ring-banana focus:outline-none"
				></textarea>
				<p id="report-comment-hint" class="text-sm text-cream-muted">
					{m.report_comment_hint({ max: MAX_COMMENT })}
				</p>
			</div>

			<p class="text-sm text-cream-muted">{m.report_note()}</p>

			<div aria-live="polite" class="min-h-5">
				{#if d.status === 'sending'}
					<p role="status" class="text-sm font-bold text-cream-muted">
						{m.report_status_sending()}
					</p>
				{:else if d.status === 'failed' && d.error}
					<p role="alert" class="text-base font-bold text-strawberry">
						{m.report_failed({ reason: errorMessage(d.error) })}
					</p>
				{/if}
			</div>
		{:else if step.name === 'block'}
			{@const person = step.person}
			<ul class="flex list-disc flex-col gap-2 pl-5 text-lg">
				<li>{m.block_leave()}</li>
				<li>{m.block_blocked({ name: person.name })}</li>
				<li>{m.block_undo()}</li>
				<li>{m.block_silent({ name: person.name })}</li>
			</ul>
			<div aria-live="polite" class="min-h-5">
				{#if blocking}
					<p role="status" class="text-sm font-bold text-cream-muted">{m.block_working()}</p>
				{:else if blockError}
					<p role="alert" class="text-base font-bold text-strawberry">
						{m.block_failed({ reason: errorMessage(blockError) })}
					</p>
				{/if}
			</div>
		{/if}
	</div>

	{#snippet footer()}
		{#if step.name === 'report'}
			{@const d = step.draft}
			<div class="flex gap-2">
				<Button tone="plain" size="md" onclick={back}>
					{step.back ? m.report_back() : m.report_cancel()}
				</Button>
				<Button
					tone="banana"
					size="md"
					disabled={d.status === 'sending'}
					aria-busy={d.status === 'sending'}
					onclick={send}
				>
					{d.status === 'sending'
						? m.report_sending()
						: d.status === 'failed'
							? m.report_retry()
							: m.report_send()}
				</Button>
			</div>
		{:else if step.name === 'block'}
			{@const person = step.person}
			<div class="flex gap-2">
				<Button tone="plain" size="md" data-autofocus disabled={blocking} onclick={back}>
					{m.report_cancel()}
				</Button>
				<Button
					tone="strawberry"
					size="md"
					disabled={blocking}
					aria-busy={blocking}
					onclick={() => confirmBlock(person)}
				>
					{blocking ? m.block_working() : m.block_confirm()}
				</Button>
			</div>
		{:else if step.name === 'menu' && step.back}
			<Button tone="plain" size="md" onclick={back}>{m.report_back()}</Button>
		{/if}
	{/snippet}
</Sheet>
