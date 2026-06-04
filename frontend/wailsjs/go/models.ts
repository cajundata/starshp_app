export namespace appapi {
	
	export class EventDTO {
	    id: string;
	    turnId: string;
	    runId?: string;
	    kind: string;
	    text?: string;
	    toolCallId?: string;
	    toolName?: string;
	    toolInput?: number[];
	    toolMetadata?: number[];
	    toolLatencyMs?: number;
	    isError?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new EventDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.turnId = source["turnId"];
	        this.runId = source["runId"];
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.toolCallId = source["toolCallId"];
	        this.toolName = source["toolName"];
	        this.toolInput = source["toolInput"];
	        this.toolMetadata = source["toolMetadata"];
	        this.toolLatencyMs = source["toolLatencyMs"];
	        this.isError = source["isError"];
	    }
	}

}

export namespace library {
	
	export class Item {
	    filename: string;
	    name: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new Item(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filename = source["filename"];
	        this.name = source["name"];
	        this.error = source["error"];
	    }
	}

}

export namespace provider {
	
	export class ModelInfo {
	    display: string;
	    id: string;
	    provider: string;
	    maxContext?: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.display = source["display"];
	        this.id = source["id"];
	        this.provider = source["provider"];
	        this.maxContext = source["maxContext"];
	    }
	}

}

export namespace store {
	
	export class Assignment {
	    ID: string;
	    SourceDir: string;
	    Title: string;
	    ManifestHash: string;
	    Model: string;
	    GroundingScope: string;
	    Status: string;
	    TotalItems: number;
	    CreatedAt: number;
	    UpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Assignment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.SourceDir = source["SourceDir"];
	        this.Title = source["Title"];
	        this.ManifestHash = source["ManifestHash"];
	        this.Model = source["Model"];
	        this.GroundingScope = source["GroundingScope"];
	        this.Status = source["Status"];
	        this.TotalItems = source["TotalItems"];
	        this.CreatedAt = source["CreatedAt"];
	        this.UpdatedAt = source["UpdatedAt"];
	    }
	}
	export class AssignmentItem {
	    ID: string;
	    AssignmentID: string;
	    Seq: number;
	    SourcePath: string;
	    Type: string;
	    Title: string;
	    RunID: string;
	    ConversationID: string;
	    Status: string;
	    Confidence: string;
	    AnswerJSON: string;
	    FlagsJSON: string;
	    AnswerPath: string;
	    Error: string;
	    CreatedAt: number;
	    UpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new AssignmentItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.AssignmentID = source["AssignmentID"];
	        this.Seq = source["Seq"];
	        this.SourcePath = source["SourcePath"];
	        this.Type = source["Type"];
	        this.Title = source["Title"];
	        this.RunID = source["RunID"];
	        this.ConversationID = source["ConversationID"];
	        this.Status = source["Status"];
	        this.Confidence = source["Confidence"];
	        this.AnswerJSON = source["AnswerJSON"];
	        this.FlagsJSON = source["FlagsJSON"];
	        this.AnswerPath = source["AnswerPath"];
	        this.Error = source["Error"];
	        this.CreatedAt = source["CreatedAt"];
	        this.UpdatedAt = source["UpdatedAt"];
	    }
	}
	export class Conversation {
	    id: string;
	    title: string;
	    createdAt: number;
	    updatedAt: number;
	    pinnedModel: string;
	
	    static createFrom(source: any = {}) {
	        return new Conversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.pinnedModel = source["pinnedModel"];
	    }
	}
	export class Message {
	    id: string;
	    conversationId: string;
	    role: string;
	    content: string;
	    model: string;
	    createdAt: number;
	    ragContext: string;
	    ragSources: string;
	    inputTokens?: number;
	    outputTokens?: number;
	    cachedInputTokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.conversationId = source["conversationId"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.model = source["model"];
	        this.createdAt = source["createdAt"];
	        this.ragContext = source["ragContext"];
	        this.ragSources = source["ragSources"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedInputTokens = source["cachedInputTokens"];
	    }
	}
	export class TextbookScope {
	    name: string;
	    chapters: number[];
	
	    static createFrom(source: any = {}) {
	        return new TextbookScope(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.chapters = source["chapters"];
	    }
	}

}

export namespace textbooks {
	
	export class Chapter {
	    num: number;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new Chapter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.num = source["num"];
	        this.path = source["path"];
	    }
	}
	export class Book {
	    name: string;
	    chapters: Chapter[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Book(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.chapters = this.convertValues(source["chapters"], Chapter);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

