import os
from datetime import datetime
from flask import Flask, request, jsonify, abort
from flask_sqlalchemy import SQLAlchemy


app = Flask(__name__)
app.config["SQLALCHEMY_DATABASE_URI"] = os.getenv(
    "SQLALCHEMY_DATABASE_URI", "sqlite:///chat.db"
)
app.config["SQLALCHEMY_TRACK_MODIFICATIONS"] = False
db = SQLAlchemy(app)


# Models
class Chat(db.Model):
    id = db.Column(db.Integer, primary_key=True)
    summary = db.Column(db.String(1024), nullable=False, default="")
    model = db.Column(db.String(1024), nullable=False)
    status = db.Column(db.String(50), nullable=False, default="idle")
    llm_params = db.Column(db.Text, nullable=False, default="{}")
    sampling_params = db.Column(db.Text, nullable=False, default="{}")
    created_timestamp = db.Column(db.DateTime, default=datetime.now)
    messages = db.relationship(
        "Message", backref="chat", lazy=True, cascade="all, delete-orphan"
    )

    def to_dict(self):
        return {
            "id": self.id,
            "summary": self.summary,
            "model": self.model,
            "status": self.status,
            "llm_params": self.llm_params,
            "sampling_params": self.sampling_params,
            "created_timestamp": self.created_timestamp.isoformat(),
            # "messages": [message.to_dict() for message in self.messages],
        }


class Message(db.Model):
    id = db.Column(db.Integer, primary_key=True)
    chat_id = db.Column(
        db.Integer, db.ForeignKey("chat.id", ondelete="CASCADE"), nullable=False
    )
    text = db.Column(db.Text, nullable=False)
    sender_type = db.Column(db.String(50), nullable=False)
    timestamp = db.Column(db.DateTime, default=datetime.now)

    def to_dict(self):
        return {
            "id": self.id,
            "chat_id": self.chat_id,
            "text": self.text,
            "sender_type": self.sender_type,
            "timestamp": self.timestamp.isoformat(),
        }


# Create the database tables
with app.app_context():
    db.create_all()


# Helper functions
def paginate_query(query):
    page = request.args.get("page", 1, type=int)
    per_page = request.args.get("per_page", 100, type=int)
    paginated_query = query.paginate(page=page, per_page=per_page, error_out=False)
    return {
        "items": [item.to_dict() for item in paginated_query.items],
        "total": paginated_query.total,
        "pages": paginated_query.pages,
        "page": page,
    }


def order_query(query, field, default_order):
    order = request.args.get("order", default_order, type=str)
    if order == "desc":
        return query.order_by(field.desc())
    return query.order_by(field.asc())


def validate_message_data(data, required_fields):
    if not data:
        abort(400, description="Request data is missing.")
    for field in required_fields:
        if field not in data:
            abort(400, description=f"Missing '{field}' in request data.")


# API Endpoints
@app.route("/chats", methods=["GET"])
def get_chats():
    chats_query = order_query(Chat.query, Chat.created_timestamp, "desc")
    return jsonify(paginate_query(chats_query))


@app.route("/chats", methods=["POST"])
def create_chat():
    chat = Chat()
    chat.model = request.args.get("model", "allenai/tulu-2-dpo-13b", type=str)
    chat.sampling_params = request.args.get("sampling_params", "{}", type=str)
    chat.llm_params = request.args.get("llm_params", "{}", type=str)
    db.session.add(chat)
    db.session.commit()
    return jsonify({"id": chat.id}), 201


@app.route("/chats/<int:chat_id>", methods=["PUT"])
def update_chat(chat_id):
    chat = Chat.query.get_or_404(chat_id)
    data = request.get_json()
    if not data:
        abort(400, description="Request data is missing.")
    changed = False
    if "summary" in data:
        chat.summary = data["summary"][:256]
        changed = True
    if "status" in data:
        chat.status = data["status"]
        changed = True
    # if "sampling_params" in data:
    #     chat.sampling_params = data["sampling_params"]
    #     changed = True
    if changed:
        db.session.commit()
    return jsonify(chat.to_dict())


@app.route("/chats/<int:chat_id>", methods=["GET"])
def get_chat(chat_id):
    chat = Chat.query.get_or_404(chat_id)
    return jsonify(chat.to_dict())


@app.route("/chats/<int:chat_id>", methods=["DELETE"])
def delete_chat(chat_id):
    chat = Chat.query.get_or_404(chat_id)
    db.session.delete(chat)
    db.session.commit()
    return jsonify({"message": "Chat deleted"}), 200


@app.route("/chats/<int:chat_id>/messages", methods=["GET"])
def get_messages(chat_id):
    Chat.query.get_or_404(chat_id)  # Ensure chat exists
    messages_query = Message.query.filter_by(chat_id=chat_id)
    messages_query = order_query(messages_query, Message.timestamp, "desc")
    return jsonify(paginate_query(messages_query))


@app.route("/chats/<int:chat_id>/messages", methods=["POST"])
def create_message(chat_id):
    chat = Chat.query.get_or_404(chat_id)  # Ensure chat exists
    data = request.get_json()
    validate_message_data(data, ["text", "sender_type"])
    message = Message(
        chat_id=chat_id, text=data["text"], sender_type=data["sender_type"]
    )

    # If this is the first message in the chat, set the summary to the message text
    if not chat.messages:
        chat.summary = data["text"]

    db.session.add(message)
    db.session.commit()
    return jsonify(message.to_dict()), 201


@app.route("/messages/<int:message_id>", methods=["GET"])
def get_message(message_id):
    message = Message.query.get_or_404(message_id)
    return jsonify(message.to_dict())


@app.route("/messages/<int:message_id>", methods=["PUT"])
def update_message(message_id):
    message = Message.query.get_or_404(message_id)
    data = request.get_json()
    validate_message_data(data, ["text"])
    message.text = data["text"]
    db.session.commit()
    return jsonify(message.to_dict())


@app.route("/messages/<int:message_id>", methods=["DELETE"])
def delete_message(message_id):
    message = Message.query.get_or_404(message_id)
    db.session.delete(message)
    db.session.commit()
    return jsonify({"message": "Message deleted"}), 200


# Error handling
@app.errorhandler(400)
def bad_request(error):
    return jsonify({"error": "Bad request", "message": error.description}), 400


@app.errorhandler(404)
def not_found(error):
    return jsonify({"error": "Not found"}), 404


@app.route("/<path:path>", methods=["OPTIONS"])
def handle_options(path):
    response = app.make_default_options_response()
    response.headers.add("Access-Control-Allow-Origin", "*")
    response.headers.add(
        "Access-Control-Allow-Headers",
        "Content-Type,Authorization,Origin,X-Requested-With,Accept,Accept-Language,Content-Language",
    )
    response.headers.add("Access-Control-Allow-Methods", "GET,PUT,POST,DELETE,OPTIONS")
    return response


# This is how we allow all origins to access our API
# This is not secure and should not be used in production
@app.after_request
def after_request(response):
    response.headers.add("Access-Control-Allow-Origin", "*")
    response.headers.add(
        "Access-Control-Allow-Headers",
        "Content-Type,Authorization,Origin,X-Requested-With,Accept,Accept-Language,Content-Language",
    )
    response.headers.add("Access-Control-Allow-Methods", "GET,PUT,POST,DELETE,OPTIONS")
    return response


if __name__ == "__main__":
    app.run(
        host="0.0.0.0", port=7025, debug=False
    )  # Turn off debug mode for production
