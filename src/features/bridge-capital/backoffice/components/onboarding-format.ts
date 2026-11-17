export function formatTimestamp(value?: string): string {
  if (!value) return 'Not provided';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export function submissionStatusLabel(status: string): string {
  switch (status) {
    case 'PENDING': return 'Pending processing';
    case 'ACCEPTED': return 'Accepted for processing (not final approval)';
    case 'FAILED': return 'Failed';
    case 'UNRESOLVED': return 'Outcome unresolved';
    default: return status;
  }
}
