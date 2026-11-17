import { useCallback } from 'react';
import { OnboardingApplications } from '../components/OnboardingApplications.js';
import type { ApplicationsListState, View } from '../useBackOffice.js';

export function ApplicationsScreen({
  listState,
  go,
}: {
  listState: ApplicationsListState;
  go: (view: View) => void;
}) {
  const pageChange = useCallback((nextState: ApplicationsListState) => {
    go({ screen: 'applications', listState: copyListState(nextState) });
  }, [go]);

  const openApplication = useCallback((applicationId: string) => {
    go({ screen: 'application', applicationId, listState: copyListState(listState) });
  }, [go, listState]);

  return (
    <OnboardingApplications
      listState={listState}
      onPageChange={pageChange}
      onOpenApplication={openApplication}
    />
  );
}

function copyListState(state: ApplicationsListState): ApplicationsListState {
  return state.cursor === undefined
    ? { previousCursors: [...state.previousCursors] }
    : { cursor: state.cursor, previousCursors: [...state.previousCursors] };
}
