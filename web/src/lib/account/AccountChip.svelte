<script lang="ts">
	import { account } from '$lib/account';
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import { m } from '$lib/paraglide/messages';
	import { profile } from '$lib/profile.svelte';

	/** Who is signed in (saved avatar + Google display name). Renders nothing for a guest. */
	const name = $derived(account.user?.name || m.account_fallback_name());
</script>

{#if account.loggedIn}
	<p
		class="flex min-h-12 min-w-0 items-center gap-2 rounded-full bg-night-2 py-1 pr-4 pl-1 ring-1 ring-night-3"
	>
		<AvatarBadge avatar={profile.avatar} size={40} />
		<span class="sr-only">{m.account_signed_in_as({ name })}</span>
		<span class="truncate font-bold" aria-hidden="true">{name}</span>
	</p>
{/if}
