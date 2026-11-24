export const operationsModelId = "gpt-6-luna";
export const operationsOutputLimit = 768;

export function singleOperationsFetch(send: typeof fetch, apiKey: string): typeof fetch {
  let used = false;
  return async (input, init) => {
    const request = new Request(input, init);
    if (used || request.method !== "POST" || request.url !== "https://api.openai.com/v1/responses") {
      throw new Error("Operations provider request denied.");
    }
    // Set before inspecting the asynchronous request body.
    used = true;
    const raw = await request.clone().text();
    if (Buffer.byteLength(raw) > 64 * 1024) throw new Error("Operations provider request too large.");
    const body = JSON.parse(raw);
    const fields = new Set(["model", "input", "max_output_tokens", "text", "store", "service_tier",
      "reasoning", "stream", "tool_choice", "tools", "include"]);
    const textOnly = Array.isArray(body.input) && body.input.length > 0 &&
      body.input.every((message: { role?: string; type?: string; content?: string | { type: string; text: string }[] }) =>
        (!message.type || message.type === "message") && ["system", "developer", "user"].includes(message.role ?? "") &&
        Object.keys(message).every(key => key === "type" || key === "role" || key === "content") &&
        (typeof message.content === "string" || (Array.isArray(message.content) &&
          message.content.every(part => part?.type === "input_text" && typeof part.text === "string" && Object.keys(part).length === 2))));
    if (body.model !== operationsModelId || body.store !== false || body.service_tier !== "default" ||
        !Number.isSafeInteger(body.max_output_tokens) || body.max_output_tokens < 16 ||
        body.max_output_tokens > operationsOutputLimit || body.background || body.previous_response_id ||
        body.tools?.length || !textOnly || Object.keys(body).some(field => !fields.has(field)) ||
        body.text?.format?.type !== "json_schema" || body.text.format.strict !== true) {
      throw new Error("Operations provider request exceeded its reviewed limits.");
    }
    // Mastra can resolve a custom endpoint with a placeholder key.
    request.headers.set("Authorization", "Bearer " + apiKey);
    const response = await send(request, { redirect: "error" });
    let received = 0;
    const stream = response.body?.pipeThrough(new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, controller) {
        received += chunk.length;
        if (received > 512 * 1024) throw new Error("Operations provider response too large.");
        controller.enqueue(chunk);
      },
    }));
    return new Response(stream, { status: response.status, statusText: response.statusText, headers: response.headers });
  };
}

export function writeOperationFrame(value: unknown): Promise<void> {
  return new Promise((resolve, reject) => {
    process.stdout.write(JSON.stringify(value) + "\n", error => error ? reject(error) : resolve());
  });
}
