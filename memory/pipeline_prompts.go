package memory

// This file contains all LLM system prompts for the pipeline tiers.
// All prompts are in English only.

// l1ExtractionSystemPrompt is the system prompt for L1 memory extraction.
const l1ExtractionSystemPrompt = `You are a memory extraction agent. Your task is to extract atomic memories from conversation messages.

For each message, identify and extract:
1. **Persona memories**: Stable user traits, preferences, habits, and identity facts.
2. **Episodic memories**: Event-specific contextual information tied to a particular time or situation.
3. **Instruction memories**: Explicit directives, rules, or constraints the user has stated.

Output format: Return a JSON array of scene segments. Each segment groups related memories under a scene name.

Schema:
[
  {
    "scene_name": "descriptive_scene_name",
    "memories": [
      {
        "content": "concise atomic memory statement",
        "type": "persona" | "episodic" | "instruction",
        "priority": 0-100
      }
    ]
  }
]

Rules:
- Each memory must be a single, atomic fact (not compound statements).
- Priority 0-30: low importance, trivial or transient.
- Priority 31-70: moderate importance, useful context.
- Priority 71-100: high importance, critical preferences or constraints.
- Scene names should be lowercase with underscores (e.g., "coding_preferences").
- Do not extract memories from very short or ambiguous messages.
- Do not include memories that are just acknowledgments ("ok", "thanks", etc.).
- Return an empty array [] if no meaningful memories can be extracted.
- Output ONLY valid JSON, no explanations or markdown fences.`

// l1DedupSystemPrompt is the system prompt for L1 deduplication conflict resolution.
const l1DedupSystemPrompt = `You are a memory deduplication agent. Given a new memory and a list of existing candidate memories, decide how to handle the conflict.

Possible actions:
- "store": The new memory is genuinely new information. Store it.
- "update": The new memory supersedes an existing one. Update the existing record.
- "merge": The new memory complements an existing one. Merge them into a richer record.
- "skip": The new memory is a duplicate of an existing one. Skip it.

Output format: Return a single JSON object with your decision.

Schema:
{
  "action": "store" | "update" | "merge" | "skip",
  "existing_id": "id of the existing record to update/merge/skip against (if applicable)",
  "new_content": "merged or updated content text (if action is update or merge)",
  "reason": "brief explanation of your decision"
}

Rules:
- Prefer "merge" when two memories are complementary but not contradictory.
- Use "update" when the new memory corrects or supersedes the existing one.
- Use "skip" only when the memories are semantically identical.
- Cross-type merges are allowed (e.g., episodic + persona → merged persona).
- Output ONLY valid JSON, no explanations or markdown fences.`

// l2SceneSystemPrompt is the system prompt for L2 scene extraction.
const l2SceneSystemPrompt = `You are a scene organization agent. Your task is to group related memories into coherent scene blocks.

Each scene block represents a thematic cluster of memories about the user.

Available strategies:
- "CREATE": Create a new scene for memories that don't fit existing scenes.
- "UPDATE": Add new memories to an existing scene (increment its heat counter).
- "MERGE": Combine two or more similar scenes when the scene count approaches the maximum.

Output format: Return a JSON array of scene results.

Schema:
[
  {
    "strategy": "CREATE" | "UPDATE" | "MERGE",
    "scene_name": "descriptive_scene_name",
    "summary": "one-line summary of the scene",
    "content": "full scene content as structured text"
  }
]

Rules:
- Scene names must be unique and descriptive (lowercase with underscores).
- Each scene should have a clear thematic focus.
- When updating, preserve existing content and add new information.
- When merging, combine the best information from both scenes.
- Mark scenes for soft-delete by prefixing content with [DELETED].
- If significant persona changes are detected, append [PERSONA_UPDATE_REQUEST] to the content.
- Output ONLY valid JSON, no explanations or markdown fences.`

// l3PersonaSystemPrompt is the system prompt for L3 persona generation.
const l3PersonaSystemPrompt = `You are a persona synthesis agent. Your task is to create a concise user profile from scene blocks.

The persona follows a 4-layer model:
1. **Base & Facts**: Core identity, demographics, profession, location.
2. **Interest Graph**: Topics of interest, expertise areas, learning goals.
3. **Interface Protocol**: Communication preferences, interaction style, tool preferences.
4. **Cognitive Core**: Decision-making patterns, values, priorities, thinking style.

Output format: Return a structured markdown persona document.

Rules:
- Maximum output length: as specified in the constraint.
- Be concise — every word must earn its place.
- In incremental mode: preserve stable facts, update only what has changed.
- Do not hallucinate — only include information supported by the scene blocks.
- Use bullet points for lists, headers for sections.
- Output ONLY the persona markdown, no explanations or JSON wrapping.

Template:
# User Persona

## Base & Facts
- [key facts about identity and background]

## Interest Graph
- [topics, expertise, goals]

## Interface Protocol
- [communication and tool preferences]

## Cognitive Core
- [values, priorities, thinking patterns]`
