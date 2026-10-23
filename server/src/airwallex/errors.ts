/** HTTP error returned by an Airwallex API request. */
export class ProviderHttpError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = "ProviderHttpError";
  }
}
