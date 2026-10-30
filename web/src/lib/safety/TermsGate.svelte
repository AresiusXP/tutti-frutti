<script lang="ts">
	import { errorMessage } from '$lib/errors';
	import { i18n } from '$lib/i18n.svelte';
	import Button from '$lib/components/Button.svelte';
	import Sheet from '$lib/lobby/Sheet.svelte';
	import { m } from '$lib/paraglide/messages';
	import { terms as shared, type TermsStore } from './terms.svelte';

	/**
	 * The terms gate: a sheet shown (once, from the layout) whenever an intent that needs the acceptance is
	 * waiting for it: creating or joining a game. Plain-language summary, a link to the full terms (a real
	 * page load in the same browsing context, so the Android app can come back), the required "13 or older
	 * and accept" box and the Accept button. Closing it (Not now, Escape, Back) declines: whoever waited is
	 * told so and nothing is created or joined. See `terms.svelte.ts`.
	 */
	let { store = shared }: { store?: TermsStore } = $props();

	let checked = $state(false);
	let needsTick = $state(false);
	let box = $state<HTMLInputElement>();

	// A new version appeared while the gate was open: it must be read and accepted again.
	$effect(() => {
		void store.requiredVersion;
		checked = false;
		needsTick = false;
	});

	async function accept() {
		if (!checked) {
			needsTick = true;
			box?.focus();
			return;
		}
		needsTick = false;
		await store.accept();
	}
</script>

{#if store.open}
	<Sheet
		title={m.terms_gate_title()}
		onclose={() => store.dismiss()}
		initialFocus="[data-autofocus]"
	>
		<div class="flex flex-col gap-3 pb-2" data-testid="terms-gate">
			<p class="text-lg">{m.terms_gate_intro()}</p>
			<ul class="flex list-disc flex-col gap-1.5 pl-5 text-base">
				<li>{m.terms_gate_rule_kind()}</li>
				<li>{m.terms_gate_rule_report()}</li>
				<li>{m.terms_gate_rule_ai()}</li>
			</ul>

			<div class="flex flex-wrap items-center justify-between gap-x-3">
				<!-- A real page load in this same browsing context (no target): the Android app comes back with Back. -->
				<a
					href="/terms#{i18n.locale}"
					rel="external"
					class="flex min-h-12 items-center font-bold text-banana underline underline-offset-4"
				>
					{m.terms_gate_read()}
				</a>
				{#if store.requiredVersion}
					<span class="text-sm text-cream-muted">
						{m.terms_gate_version({ version: store.requiredVersion })}
					</span>
				{/if}
			</div>

			{#if store.updated}
				<p role="status" class="rounded-xl bg-night px-3 py-2 font-bold text-banana">
					{m.terms_gate_updated()}
				</p>
			{/if}

			<label
				for="terms-age"
				class="flex min-h-12 cursor-pointer items-center gap-3 rounded-xl bg-night px-3 py-2 text-base font-bold ring-2 {needsTick
					? 'ring-strawberry'
					: 'ring-night-3'}"
			>
				<input
					id="terms-age"
					name="terms-age"
					type="checkbox"
					autocomplete="off"
					required
					bind:checked
					bind:this={box}
					data-autofocus
					aria-describedby={needsTick ? 'terms-age-error' : undefined}
					class="size-6 shrink-0 accent-banana"
				/>
				{m.terms_gate_age_label()}
			</label>

			<div aria-live="polite" class="min-h-5">
				{#if needsTick && !checked}
					<p id="terms-age-error" role="alert" class="text-sm font-bold text-strawberry">
						{m.terms_gate_check_required()}
					</p>
				{:else if store.saving}
					<p role="status" class="text-sm font-bold text-cream-muted">{m.terms_gate_accepting()}</p>
				{:else if store.error}
					<p role="alert" class="text-base font-bold text-strawberry">
						{m.terms_gate_failed({ reason: errorMessage(store.error) })}
					</p>
				{/if}
			</div>
		</div>

		{#snippet footer()}
			<div class="flex gap-2">
				<Button tone="plain" size="md" disabled={store.saving} onclick={() => store.dismiss()}>
					{m.terms_gate_cancel()}
				</Button>
				<Button
					tone="banana"
					size="md"
					disabled={store.saving}
					aria-busy={store.saving}
					onclick={accept}
				>
					{store.saving
						? m.terms_gate_accepting()
						: store.error
							? m.terms_gate_retry()
							: m.terms_gate_accept()}
				</Button>
			</div>
		{/snippet}
	</Sheet>
{/if}
