import { z } from "zod";

const errorSchema = z.object({
  error: z.object({
    message: z.string(),
    type: z.string().nullish(),
    code: z.union([z.string(), z.number()]).nullish(),
    param: z.unknown().optional(),
  }),
});

export const proTerminalErrorStructure = {
  errorSchema,
  errorToMessage: (data: z.infer<typeof errorSchema>): string => data.error.message,
  isRetryable: (response: Response, data?: z.infer<typeof errorSchema>): boolean => {
    if (response.status === 502 && data?.error.code === "incomplete_upstream_response") {
      return false;
    }
    return [408, 409, 429].includes(response.status) || response.status >= 500;
  },
};
