export namespace provider {
	
	export class ModelInfo {
	    display: string;
	    id: string;
	    provider: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.display = source["display"];
	        this.id = source["id"];
	        this.provider = source["provider"];
	    }
	}

}

export namespace store {
	
	export class Conversation {
	    id: string;
	    title: string;
	    createdAt: number;
	    updatedAt: number;
	    presetId: string;
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
	        this.presetId = source["presetId"];
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
	    }
	}
	export class Preset {
	    id: string;
	    name: string;
	    systemPrompt: string;
	    createdAt: number;
	    updatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Preset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.systemPrompt = source["systemPrompt"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
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
	
	    static createFrom(source: any = {}) {
	        return new Book(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.chapters = this.convertValues(source["chapters"], Chapter);
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

