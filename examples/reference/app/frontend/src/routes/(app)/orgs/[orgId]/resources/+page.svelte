<!-- Generated once by nise; owned by this application. Nise will not overwrite it. -->
<script lang="ts">
	import { page } from '$app/state';

	import Badge from '$lib/components/ui/Badge.svelte';
	import Button from '$lib/components/ui/Button.svelte';
	import Checkbox from '$lib/components/ui/Checkbox.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Pagination from '$lib/components/ui/Pagination.svelte';
	import ProblemAlert from '$lib/components/ui/ProblemAlert.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Table from '$lib/components/ui/Table.svelte';
	import { listResources, type ResourcePage } from '$lib/features/workbench/api';
	import { session } from '$lib/session.svelte';
	import { listHref, listQuery, readListState } from '$lib/table/list-state';

	const orgId = $derived(page.params.orgId ?? '');

	// An action the session cannot perform is not offered. A courtesy, never a
	// control: the server refuses the request either way, and a button hidden
	// from somebody who could not use it is kinder than one that fails.
	const mayManage = $derived(session.can('resources.manage'));

	// The filters this list understands. Naming them is what keeps an
	// unrelated parameter out of the request, and therefore out of the cursor
	// binding the server derives from it — a cursor is refused when the query
	// it was issued for changes, so a stray parameter would break paging.
	const filters = ['include_retired'] as const;

	const listState = $derived(readListState(page.url, filters));
	const includeRetired = $derived(listState.filters.include_retired === 'true');

	let result = $state<ResourcePage | null>(null);
	let failure = $state<unknown>(null);

	// Reloading on every URL change is what makes the back button work: a list
	// that loaded once and then mutated its own state would show the previous
	// page's rows under the previous page's URL.
	$effect(() => {
		const query = listQuery(listState);
		const controller = new AbortController();
		result = null;
		failure = null;
		listResources(orgId, query, controller.signal)
			.then((loaded) => (result = loaded))
			.catch((error) => {
				// An abort is this effect being superseded, not a failure to
				// report: nobody is waiting for the request it cancelled.
				if (error instanceof Error && error.name === 'AbortError') return;
				failure = error;
			});
		return () => controller.abort();
	});

	const nextHref = $derived(
		result?.page.next_cursor
			? listHref(page.url, { after: result.page.next_cursor }, filters)
			: undefined
	);
	const retiredToggleHref = $derived(
		listHref(page.url, { filters: { include_retired: includeRetired ? '' : 'true' } }, filters)
	);
</script>

<svelte:head>
	<title>Equipment · Workbench</title>
</svelte:head>

<div class="mx-auto max-w-5xl px-4 py-8 sm:px-6">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div>
			<h1 class="text-2xl font-semibold tracking-tight text-text">Equipment</h1>
			<p class="mt-1 text-sm text-text-muted">
				{#if result}
					{result.total}
					{result.total === 1 ? 'resource' : 'resources'}
				{/if}
			</p>
		</div>
		{#if mayManage}
			<Button variant="primary" href="/orgs/{orgId}/resources/new">Add equipment</Button>
		{/if}
	</div>

	<!--
		The filter is a link rather than a checkbox that fires a request. Every
		piece of list state lives in the URL and nowhere else, which is what
		makes a filtered list something you can bookmark, send to a colleague,
		and reach again with the back button. The checkbox is the affordance;
		the anchor around it is the mechanism.
	-->
	<div class="mt-5">
		<a
			href={retiredToggleHref}
			class="inline-flex items-center gap-2 rounded-xs text-sm text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
		>
			<Checkbox
				id="include-retired"
				label="Include retired equipment"
				checked={includeRetired}
				readonly
				tabindex={-1}
				aria-hidden="true"
			/>
		</a>
	</div>

	{#if failure}
		<div class="mt-6"><ProblemAlert error={failure} /></div>
	{:else if result === null}
		<div class="mt-6 space-y-2" aria-busy="true">
			<Skeleton class="h-10 w-full" />
			<Skeleton class="h-10 w-full" />
			<Skeleton class="h-10 w-full" />
		</div>
	{:else if result.items.length === 0}
		<div class="mt-6">
			<EmptyState
				title={includeRetired ? 'No equipment yet' : 'No equipment in service'}
				description={includeRetired
					? 'Add the first piece of shared equipment.'
					: 'Everything here has been retired. Include retired equipment to see it.'}
			>
				{#snippet action()}
					{#if !includeRetired}
						<Button href={retiredToggleHref}>Include retired</Button>
					{:else if mayManage}
						<Button variant="primary" href="/orgs/{orgId}/resources/new">Add equipment</Button>
					{/if}
				{/snippet}
			</EmptyState>
		</div>
	{:else}
		<div class="mt-6">
			<Table caption="Shared equipment, by name">
				<thead class="border-b border-line bg-surface-muted text-left">
					<tr>
						<th scope="col" class="px-4 py-2 font-medium text-text-muted">Name</th>
						<th scope="col" class="hidden px-4 py-2 font-medium text-text-muted sm:table-cell">
							Location
						</th>
						<th scope="col" class="px-4 py-2 font-medium text-text-muted">Status</th>
						<th scope="col" class="px-4 py-2"><span class="sr-only">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each result.items as item (item.id)}
						<tr class="border-b border-line last:border-0">
							<td class="px-4 py-2">
								<a
									class="rounded-xs font-medium text-primary underline underline-offset-4 hover:text-primary-hover"
									href="/orgs/{orgId}/resources/{item.id}">{item.name}</a
								>
								<!--
									The location repeats here on a narrow screen, where its
									own column is hidden. Hiding a column outright would
									lose the information; moving it under the name keeps
									the table readable on a phone without a horizontal
									scroll nobody finds.
								-->
								{#if item.location}
									<span class="block text-sm text-text-muted sm:hidden">{item.location}</span>
								{/if}
							</td>
							<td class="hidden px-4 py-2 text-text-muted sm:table-cell">{item.location}</td>
							<td class="px-4 py-2">
								{#if item.in_service}
									<Badge tone="success">In service</Badge>
								{:else}
									<Badge tone="neutral">Retired</Badge>
								{/if}
							</td>
							<td class="px-4 py-2 text-right">
								<Button size="sm" variant="ghost" href="/orgs/{orgId}/reservations?resource_id={item.id}">
									<!--
										The accessible name says which resource. "Calendar"
										repeated down a column is a screen-reader user
										hearing the same link eleven times.
									-->
									<span aria-hidden="true">Calendar</span>
									<span class="sr-only">Calendar for {item.name}</span>
								</Button>
							</td>
						</tr>
					{/each}
				</tbody>
			</Table>
		</div>

		<Pagination
			label="Equipment"
			{nextHref}
			summary={`Showing ${result.items.length} of ${result.total}`}
		/>
	{/if}
</div>
